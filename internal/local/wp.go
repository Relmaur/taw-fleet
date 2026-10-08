package local

import (
	"errors"
	"fmt"

	"github.com/Relmaur/taw-fleet/internal/exec"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/site"
)

// ErrSiteHalted means the site's database isn't up, so wp-cli can't run.
var ErrSiteHalted = errors.New("site isn't running")

// WPSpec is the wp-cli command for a site: the site's PHP from Local (else
// php on PATH), Local's wp-cli (else wp on PATH), and the site's MySQL
// socket, the same way taw/core's `bin/taw wp` does it.
func WPSpec(p paths.Paths, s site.Site, args []string) (exec.Spec, error) {
	if !s.SockLive {
		return exec.Spec{}, fmt.Errorf("%s: %w; start it with `taw-fleet start %s`", s.Slug, ErrSiteHalted, s.Slug)
	}
	sock := []string{
		"-d", "mysqli.default_socket=" + s.Socket,
		"-d", "pdo_mysql.default_socket=" + s.Socket,
		// PHP notices (Local's wp-cli is noisy on PHP 8.5) go to stderr, so
		// stdout stays clean for --format=json and friends.
		"-d", "display_errors=stderr",
	}
	spec := exec.Spec{Dir: s.WebRoot, Env: []string{"MYSQL_UNIX_PORT=" + s.Socket}}
	php, havePHP := PHPBinary(p, s.PHPVersion)
	if !havePHP {
		lp, err := p.LookPath("php")
		if err != nil {
			return exec.Spec{}, fmt.Errorf("no PHP %s from Local and no php on PATH", s.PHPVersion)
		}
		php = lp
	}
	wp, haveWP := WPCliPhar(p)
	if !haveWP {
		lp, err := p.LookPath("wp")
		if err != nil {
			return exec.Spec{}, errors.New("no wp-cli: neither Local's nor one on PATH")
		}
		wp = lp
	}
	spec.Name = php
	spec.Args = append(append(append(sock, wp), "--path="+s.WebRoot), args...)
	return spec, nil
}
