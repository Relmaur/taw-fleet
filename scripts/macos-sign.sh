#!/bin/sh
# Signs taw-fleet with one stable identity, so macOS keeps its privacy
# permissions (Files & Folders, Automation) across rebuilds and upgrades.
# Docs: docs/signing.md. GoReleaser runs `binary` and `app` from build hooks;
# a person runs the same commands for a dev build.
#
#   scripts/macos-sign.sh binary <file> <identifier>
#       sign one program in place (identifier: com.relmaur.taw-fleet, com.relmaur.taw)
#   scripts/macos-sign.sh app <taw-fleet binary> <version> <out dir>
#       sign the binary, then build and sign <out dir>/TAW Fleet.app around a copy of it
#   scripts/macos-sign.sh dev
#       go build into bin/ and make bin/TAW Fleet.app (the dev build)
#   scripts/macos-sign.sh identity
#       print the identity this Mac signs with, or say why there is none
#   scripts/macos-sign.sh make-cert
#       one time per owner: create the self-signed code-signing certificate in the
#       signing keychain and write a .p12 for the release's GitHub secrets
#   scripts/macos-sign.sh import <.p12> <password file>
#       another Mac (or a lost keychain): the signing keychain from that .p12
#   scripts/macos-sign.sh ci-keychain
#       in the release workflow: a temporary keychain from MACOS_SIGN_P12 (base64)
#       and MACOS_SIGN_P12_PASSWORD
#
# The identity is the certificate called $TAW_FLEET_SIGN_IDENTITY (default
# "TAW Fleet Code Signing") in its own keychain, $TAW_FLEET_SIGN_KEYCHAIN
# (default ~/Library/Keychains/taw-fleet-signing.keychain-db). That keychain's
# password is random and kept in the login keychain as the generic password
# "taw-fleet-signing-keychain", so signing never asks for the login password.
# Without the identity the command fails: an ad-hoc signature
# changes on every build, which is the bug this exists to stop. TAW_FLEET_SIGN=adhoc
# signs ad-hoc anyway (a contributor without the certificate; never a release).
set -eu

IDENTITY_NAME=${TAW_FLEET_SIGN_IDENTITY:-TAW Fleet Code Signing}
BUNDLE_ID=com.relmaur.taw-fleet
APP_NAME="TAW Fleet.app"
ROOT=$(cd "$(dirname "$0")/.." && pwd)
KEYCHAIN=${TAW_FLEET_SIGN_KEYCHAIN:-$HOME/Library/Keychains/taw-fleet-signing.keychain-db}
KEYCHAIN_ITEM=taw-fleet-signing-keychain

die() {
	printf 'macos-sign: %s\n' "$*" >&2
	exit 1
}

# unlock opens the signing keychain with the password the login keychain keeps
# for it (CI's keychain is already unlocked: no such item there).
unlock() {
	[ -f "$KEYCHAIN" ] || return 0
	if kpw=$(security find-generic-password -s "$KEYCHAIN_ITEM" -a "$USER" -w 2>/dev/null); then
		security unlock-keychain -p "$kpw" "$KEYCHAIN" || return 1
	fi
}

# identity prints the SHA-1 of the signing certificate, or fails. Untrusted
# (self-signed) identities count: `find-identity` without -v lists them, and
# codesign needs no trust to sign.
identity() {
	if [ "${TAW_FLEET_SIGN:-}" = adhoc ]; then
		echo -
		return
	fi
	[ -f "$KEYCHAIN" ] || die "no signing keychain at $KEYCHAIN. Create it once with \`scripts/macos-sign.sh make-cert\` (docs/signing.md), or set TAW_FLEET_SIGN=adhoc for a throwaway build (macOS will forget its permissions)."
	unlock || die "can't open $KEYCHAIN with its stored password (docs/signing.md)."
	hash=$(security find-identity -p codesigning "$KEYCHAIN" 2>/dev/null |
		awk -v name="\"$IDENTITY_NAME\"" 'index($0, name) && $2 ~ /^[0-9A-F]{40}$/ { print $2; exit }')
	[ -n "$hash" ] || die "no \"$IDENTITY_NAME\" certificate in $KEYCHAIN (docs/signing.md)."
	echo "$hash"
}

# codesign_with signs with the identity (from the signing keychain), or ad-hoc.
# codesign finds an identity only in a keychain on the search list, and a
# locked keychain left there makes macOS ask other apps' users for its
# password: so it's listed only while one signature runs, one at a time
# (GoReleaser's hooks run in parallel), and the list is put back after.
codesign_with() {
	hash=$(identity)
	if [ "$hash" = - ]; then
		codesign --force --timestamp=none --sign - "$@"
		return
	fi
	if security list-keychains -d user | grep -qF "\"$KEYCHAIN\""; then
		codesign --force --timestamp=none --keychain "$KEYCHAIN" --sign "$hash" "$@"
		return
	fi
	lock="${TMPDIR:-/tmp}/taw-fleet-sign.lock"
	tries=0
	until mkdir "$lock" 2>/dev/null; do
		tries=$((tries + 1))
		[ "$tries" -lt 1200 ] || die "waited 2 minutes for $lock: if no signing is running, remove that folder."
		sleep 0.1
	done
	saved=$(security list-keychains -d user)
	trap 'eval "security list-keychains -d user -s $saved"; rmdir "$lock"' EXIT INT TERM
	eval "security list-keychains -d user -s $saved \"\$KEYCHAIN\""
	status=0
	codesign --force --timestamp=none --keychain "$KEYCHAIN" --sign "$hash" "$@" || status=$?
	eval "security list-keychains -d user -s $saved"
	rmdir "$lock"
	trap - EXIT INT TERM
	return "$status"
}

sign_binary() {
	file=$1 id=$2
	[ -f "$file" ] || die "$file: not found"
	codesign_with --identifier "$id" "$file"
}

make_app() {
	bin=$1 version=$2 out=$3
	sign_binary "$bin" "$BUNDLE_ID"
	app="$out/$APP_NAME"
	rm -rf "$app"
	mkdir -p "$app/Contents/MacOS"
	cp "$bin" "$app/Contents/MacOS/taw-fleet"
	sed "s/__VERSION__/$version/g" "$ROOT/packaging/macos/Info.plist" >"$app/Contents/Info.plist"
	plutil -lint -s "$app/Contents/Info.plist"
	codesign_with "$app"
	codesign --verify --strict "$app"
}

# new_keychain <p12> <p12 password> <keychain> <keychain password>: the keychain
# holding only the signing identity, codesign allowed to use its key without a
# dialog (the partition list, set with the keychain's own password).
new_keychain() {
	security create-keychain -p "$4" "$3"
	security set-keychain-settings -lut 3600 "$3"
	security unlock-keychain -p "$4" "$3"
	security import "$1" -k "$3" -P "$2" -T /usr/bin/codesign >/dev/null
	security set-key-partition-list -S apple-tool:,apple: -s -k "$4" "$3" >/dev/null
}

# import_p12 <p12> <password file>: this Mac's signing keychain from a .p12
# (make-cert's, or the one in the release secrets on a second Mac).
import_p12() {
	[ -f "$1" ] && [ -f "$2" ] || die "usage: import <.p12> <file holding its password>"
	[ ! -e "$KEYCHAIN" ] || die "$KEYCHAIN already exists: nothing to do."
	kpw=$(/usr/bin/openssl rand -hex 24)
	new_keychain "$1" "$(cat "$2")" "$KEYCHAIN" "$kpw"
	security add-generic-password -U -s "$KEYCHAIN_ITEM" -a "$USER" -w "$kpw" \
		-l "taw-fleet signing keychain" -T /usr/bin/security
	# Prove the stored password opens it, before anything relies on that.
	security lock-keychain "$KEYCHAIN"
	unlock || die "the signing keychain doesn't open with its stored password; delete $KEYCHAIN and import again."
	echo "Signing keychain ready: $KEYCHAIN (\"$IDENTITY_NAME\" $(identity))."
}

make_cert() {
	[ ! -e "$KEYCHAIN" ] || die "$KEYCHAIN already exists: nothing to do (docs/signing.md)."
	out=${TAW_FLEET_CERT_DIR:-$(mktemp -d)}
	umask 077
	cat >"$out/cert.cnf" <<-EOF
		[req]
		distinguished_name = dn
		x509_extensions = ext
		prompt = no
		[dn]
		CN = $IDENTITY_NAME
		[ext]
		basicConstraints = critical, CA:false
		keyUsage = critical, digitalSignature
		extendedKeyUsage = critical, codeSigning
	EOF
	# /usr/bin/openssl (LibreSSL): its .p12 uses the algorithms `security import` reads.
	/usr/bin/openssl req -x509 -newkey rsa:3072 -nodes -days 3650 -config "$out/cert.cnf" \
		-keyout "$out/key.pem" -out "$out/cert.pem" 2>/dev/null
	pw=$(/usr/bin/openssl rand -hex 24)
	/usr/bin/openssl pkcs12 -export -inkey "$out/key.pem" -in "$out/cert.pem" \
		-name "$IDENTITY_NAME" -out "$out/taw-fleet-signing.p12" -passout "pass:$pw"
	rm -f "$out/key.pem" "$out/cert.cnf"
	printf '%s' "$pw" >"$out/p12-password.txt"
	import_p12 "$out/taw-fleet-signing.p12" "$out/p12-password.txt"
	echo "Created \"$IDENTITY_NAME\" (valid 10 years). Keep these two safe (a password manager), then delete the folder:"
	echo "  $out/taw-fleet-signing.p12   → secret MACOS_SIGN_P12 (base64 of it); a second Mac: import"
	echo "  $out/p12-password.txt        → secret MACOS_SIGN_P12_PASSWORD"
}

ci_keychain() {
	: "${MACOS_SIGN_P12:?the MACOS_SIGN_P12 secret is missing (docs/signing.md)}"
	: "${MACOS_SIGN_P12_PASSWORD:?the MACOS_SIGN_P12_PASSWORD secret is missing (docs/signing.md)}"
	dir=${RUNNER_TEMP:-$(mktemp -d)}
	KEYCHAIN="$dir/taw-fleet-signing.keychain-db"
	printf '%s' "$MACOS_SIGN_P12" | base64 --decode >"$dir/signing.p12"
	new_keychain "$dir/signing.p12" "$MACOS_SIGN_P12_PASSWORD" "$KEYCHAIN" "$(/usr/bin/openssl rand -hex 16)"
	rm -f "$dir/signing.p12"
	# shellcheck disable=SC2046 # one quoted path per line
	eval "security list-keychains -d user -s $(security list-keychains -d user) \"\$KEYCHAIN\""
	# GoReleaser's hooks (later steps) sign from this keychain, unlocked for an hour.
	[ -z "${GITHUB_ENV:-}" ] || echo "TAW_FLEET_SIGN_KEYCHAIN=$KEYCHAIN" >>"$GITHUB_ENV"
	echo "Signing keychain ready ($(identity))."
}

cmd=${1:-}
[ $# -gt 0 ] && shift
case "$cmd" in
binary)
	[ $# -eq 2 ] || die "usage: binary <file> <identifier>"
	sign_binary "$1" "$2"
	;;
app)
	[ $# -eq 3 ] || die "usage: app <taw-fleet binary> <version> <out dir>"
	make_app "$1" "$2" "$3"
	;;
dev)
	cd "$ROOT"
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -o bin/taw-fleet ./cmd/taw-fleet
	CGO_ENABLED=0 go build -trimpath -o bin/taw ./cmd/taw
	sign_binary bin/taw com.relmaur.taw
	make_app bin/taw-fleet dev bin
	echo "bin/taw-fleet, bin/taw and bin/$APP_NAME, signed with \"$IDENTITY_NAME\"."
	;;
identity)
	hash=$(identity)
	echo "$hash  \"$IDENTITY_NAME\""
	;;
make-cert) make_cert ;;
import)
	[ $# -eq 2 ] || die "usage: import <.p12> <file holding its password>"
	import_p12 "$1" "$2"
	;;
ci-keychain) ci_keychain ;;
*) die "usage: binary | app | dev | identity | make-cert | import | ci-keychain (see the top of this file)" ;;
esac
