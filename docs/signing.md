# Signing taw-fleet (one identity for macOS privacy)

macOS keeps privacy permissions (System Settings → Privacy & Security → Files & Folders,
Automation) per app identity. Until v1.35, taw-fleet was a plain program, ad-hoc signed by
Go's linker (`Identifier=a.out`), in a Homebrew folder named after its version. Each release
was therefore a new app to macOS. The launchd agents (`watch`, `--at-login`) run taw-fleet
on its own, so macOS asks taw-fleet itself for `~/Documents`, where the TAW themes that Local's
sites link to live. The result was one Files & Folders entry per release, and silent
"Operation not permitted" errors when a new build hadn't been allowed yet.

Since v1.35:

- **One bundle.** Homebrew installs `/Applications/TAW Fleet.app` (bundle identifier
  `com.relmaur.taw-fleet`) and links `taw-fleet` to `Contents/MacOS/taw-fleet` inside it, so the
  agents run the app. Its `Info.plist` (`packaging/macos/Info.plist`) says why taw-fleet needs
  Documents, the Desktop and Automation, so macOS asks once instead of refusing silently.
  Opening the app from Finder or Spotlight opens the dashboard in a terminal window
  (`LSEnvironment` sets `TAW_FLEET_APP=1`, which only that kind of launch gets).
- **One identity.** Every build, release or dev, is signed with the self-signed certificate
  "TAW Fleet Code Signing". The designated requirement is
  `identifier "com.relmaur.taw-fleet" and certificate leaf = H"<the certificate's SHA-1>"`, the
  same for every build, so a permission given once carries over to the next version.

`scripts/macos-sign.sh` does all of it. GoReleaser runs it from build hooks, and a person runs
the same commands.

## Where the certificate lives

In its own keychain, `~/Library/Keychains/taw-fleet-signing.keychain-db`, not the login
keychain. Its password is random and stored in the login keychain as the generic password
`taw-fleet-signing-keychain`, so signing never asks for a password. If macOS ever shows
"security wants to use the taw-fleet-signing keychain", click **Cancel**: you don't know that
password and never need it. Then tell whoever maintains this script.

codesign only finds an identity in a keychain on the search list, and a locked keychain on
the list makes macOS ask for its password whenever any app searches your keychains. So
the script adds it to the list for one signature at a time and puts the list back after.

## One time per owner

```bash
scripts/macos-sign.sh make-cert      # the certificate + the signing keychain
scripts/macos-sign.sh identity       # check: prints its SHA-1
```

`make-cert` prints the paths of a `.p12` and its password. Keep both in a password manager:
they're the only way to sign with the same identity on another Mac or in CI. Then:

```bash
base64 -i taw-fleet-signing.p12 | gh secret set MACOS_SIGN_P12 -R Relmaur/taw-fleet
gh secret set MACOS_SIGN_P12_PASSWORD -R Relmaur/taw-fleet < p12-password.txt
```

On another Mac, or after losing the keychain: `scripts/macos-sign.sh import <.p12> <password file>`.
A new certificate (`make-cert` again) is a new identity: macOS asks for each permission once
more, once.

## Builds

| Build | Command | Signed by |
|---|---|---|
| Release | tag `v*` → `.github/workflows/release.yml` (`ci-keychain` step from the two secrets, then GoReleaser) | the secrets' certificate |
| Local release check | `goreleaser release --snapshot --clean` | the signing keychain |
| Dev | `scripts/macos-sign.sh dev` → `bin/taw-fleet`, `bin/taw`, `bin/TAW Fleet.app` | the signing keychain |

Without the certificate the script fails rather than signing ad-hoc, which would bring the
bug back. `TAW_FLEET_SIGN=adhoc` signs ad-hoc anyway, for a throwaway build that macOS will
treat as a new app. A plain `go build` or `go run` is unsigned. That's fine from a terminal,
because macOS asks on the terminal's behalf, but never point the launchd agents at one.

## Check a build

```bash
codesign -dv --verbose=4 "/Applications/TAW Fleet.app"   # Identifier=com.relmaur.taw-fleet, Authority=TAW Fleet Code Signing
codesign -d -r- "/Applications/TAW Fleet.app"            # the designated requirement above
```

## Clearing permissions

`tccutil reset All com.relmaur.taw-fleet` resets every privacy permission of TAW Fleet.app
(only that app). The entries from before v1.35 belong to programs at old
`/opt/homebrew/Caskroom/taw-fleet/<version>/taw-fleet` paths under the shared identifier
`a.out`, which `tccutil` can't name without touching other programs. They're harmless (those
files are gone). Remove them by hand: select a `taw-fleet` row in Files & Folders and click −.
Do it after upgrading to v1.35 or later: until then the installed version still needs its own row.
