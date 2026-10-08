// Package createform is the "new TAW site" form (charm.land/huh): the same
// questions for `taw-fleet create` in a terminal and for n in the dashboard.
package createform

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/Relmaur/taw-fleet/internal/config"
	"github.com/Relmaur/taw-fleet/internal/create"
	"github.com/Relmaur/taw-fleet/internal/local"
	"github.com/Relmaur/taw-fleet/internal/paths"
	"github.com/Relmaur/taw-fleet/internal/style"
)

// Fields are the form's answers. Empty Domain/ThemeDir/PHP/WebServer mean
// the default.
type Fields struct {
	Name       string
	Kind       string // "classic" | "block"
	Domain     string
	ThemeDir   string
	PHP        string
	WebServer  string
	AdminUser  string
	AdminEmail string
	Confirmed  bool
}

// Defaults fills the fields from the config's [create] section.
func Defaults(c config.Create) Fields {
	f := Fields{Kind: string(create.Classic), PHP: c.PHP, WebServer: c.WebServer, AdminUser: c.AdminUser, AdminEmail: c.AdminEmail}
	if c.Kind == "block" {
		f.Kind = string(create.Block)
	}
	return f
}

// Request turns the answers into a create.Request (not normalized yet).
func (f Fields) Request() create.Request {
	return create.Request{Name: strings.TrimSpace(f.Name), Kind: create.Kind(f.Kind), Domain: strings.TrimSpace(f.Domain),
		ThemeDir: strings.TrimSpace(f.ThemeDir), PHP: f.PHP, WebServer: f.WebServer,
		AdminUser: strings.TrimSpace(f.AdminUser), AdminEmail: strings.TrimSpace(f.AdminEmail)}
}

// New builds the form over f. Every field is checked as it's left, against
// what Local already has, so the confirm step only shows a request that
// will start.
func New(p paths.Paths, dark bool, f *Fields) *huh.Form {
	slug := func() string { return create.Slugify(f.Name) }
	services := local.Services(p)

	phpOpts := []huh.Option[string]{huh.NewOption("Local's preferred", "")}
	for _, v := range services["php"] {
		phpOpts = append(phpOpts, huh.NewOption("PHP "+v, v))
	}
	webOpts := []huh.Option[string]{huh.NewOption("Local's preferred", "")}
	for _, name := range local.WebServers {
		for _, v := range services[name] {
			webOpts = append(webOpts, huh.NewOption(name+" "+v, name+"-"+v))
		}
	}

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Site name").Description("As Local shows it; the folder and domain come from it.").
				Placeholder("Acme Shop").Value(&f.Name).Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("a name, please")
				}
				r := create.Request{Name: strings.TrimSpace(s), Slug: create.Slugify(s), Domain: create.Slugify(s) + ".local"}
				if r.Slug == "" {
					return errors.New("use some letters or digits")
				}
				return create.Check(p, r)
			}),
			huh.NewSelect[string]().Title("TAW theme").Options(
				huh.NewOption("CLASSIC · taw-theme: PHP blocks, Vite, Tailwind, Alpine", string(create.Classic)),
				huh.NewOption("BLOCK · taw-gutenberg: block theme, full site editing", string(create.Block)),
			).Value(&f.Kind),
		),
		huh.NewGroup(
			huh.NewInput().Title("Domain").Value(&f.Domain).
				PlaceholderFunc(func() string { return slug() + ".local" }, &f.Name).
				DescriptionFunc(func() string { return "Empty: " + slug() + ".local" }, &f.Name).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return nil
					}
					_, err := create.Normalize(p, create.Request{Name: "x", Kind: create.Classic, Domain: s, AdminUser: "x", AdminEmail: "x@x.x", AdminPassword: "x"})
					if err != nil {
						return err
					}
					return create.Check(p, create.Request{Name: f.Name, Slug: slug(), Domain: strings.ToLower(strings.TrimSpace(s))})
				}),
			huh.NewInput().Title("Theme folder").Value(&f.ThemeDir).
				PlaceholderFunc(slug, &f.Name).
				DescriptionFunc(func() string { return "In wp-content/themes. Empty: " + slug() }, &f.Name).
				Validate(func(s string) error {
					if s = strings.TrimSpace(s); s != "" && create.Slugify(s) != s {
						return fmt.Errorf("lowercase letters, digits and dashes (%s)", create.Slugify(s))
					}
					return nil
				}),
			huh.NewSelect[string]().Title("PHP").Options(phpOpts...).Value(&f.PHP),
			huh.NewSelect[string]().Title("Web server").Options(webOpts...).Value(&f.WebServer),
		).Title("Local site"),
		huh.NewGroup(
			huh.NewInput().Title("Admin username").Value(&f.AdminUser).Validate(func(s string) error {
				_, err := create.Normalize(p, create.Request{Name: "x", Kind: create.Classic, AdminUser: strings.TrimSpace(s), AdminEmail: "x@x.x", AdminPassword: "x"})
				return err
			}),
			huh.NewInput().Title("Admin email").Value(&f.AdminEmail).Validate(func(s string) error {
				_, err := create.Normalize(p, create.Request{Name: "x", Kind: create.Classic, AdminUser: "x", AdminEmail: strings.TrimSpace(s), AdminPassword: "x"})
				return err
			}),
			huh.NewNote().Title("Password").Description("Generated for you, shown once when the site is ready."),
		).Title("WordPress admin"),
		huh.NewGroup(
			huh.NewConfirm().TitleFunc(func() string { return "Create " + strings.TrimSpace(f.Name) + "?" }, &f.Name).
				DescriptionFunc(func() string { return Summary(*f) }, f).
				Affirmative("Create").Negative("Back out").Value(&f.Confirmed),
		),
	).WithTheme(Theme(style.New(dark))).WithShowHelp(true)
	return form
}

// Header is the line above the form.
func Header(p style.Palette) string {
	return lipgloss.NewStyle().Bold(true).Foreground(p.Accent).Render("+ New TAW site") +
		p.Fg(p.Muted).Render("   a Local site with taw-theme or taw-gutenberg")
}

// Summary is the confirm step's description: what will be made, where.
func Summary(f Fields) string {
	r := f.Request()
	s := create.Slugify(r.Name)
	domain, dir := r.Domain, r.ThemeDir
	if domain == "" {
		domain = s + ".local"
	}
	if dir == "" {
		dir = s
	}
	env := "Local's preferred PHP and web server"
	if r.PHP != "" || r.WebServer != "" {
		parts := []string{}
		if r.PHP != "" {
			parts = append(parts, "PHP "+r.PHP)
		}
		if r.WebServer != "" {
			parts = append(parts, strings.Replace(r.WebServer, "-", " ", 1))
		}
		env = strings.Join(parts, ", ")
	}
	return fmt.Sprintf("http://%s · ~/Local Sites/%s · %s\n%s in wp-content/themes/%s, activated, git repo with a first commit\nAdmin %s <%s>, generated password",
		domain, s, env, create.Kind(f.Kind).Scaffold(), dir, r.AdminUser, r.AdminEmail)
}

// Theme is huh's look in taw-fleet's palette.
func Theme(p style.Palette) huh.ThemeFunc {
	return func(isDark bool) *huh.Styles {
		t := huh.ThemeBase(isDark)
		t.Focused.Base = t.Focused.Base.BorderForeground(p.Accent)
		t.Focused.Card = t.Focused.Base
		t.Focused.Title = t.Focused.Title.Foreground(p.Accent).Bold(true)
		t.Focused.NoteTitle = t.Focused.NoteTitle.Foreground(p.Accent).Bold(true)
		t.Focused.Description = t.Focused.Description.Foreground(p.Muted)
		t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Foreground(p.Err)
		t.Focused.ErrorMessage = t.Focused.ErrorMessage.Foreground(p.Err)
		t.Focused.SelectSelector = t.Focused.SelectSelector.Foreground(p.Accent)
		t.Focused.NextIndicator = t.Focused.NextIndicator.Foreground(p.Accent)
		t.Focused.PrevIndicator = t.Focused.PrevIndicator.Foreground(p.Accent)
		t.Focused.Option = t.Focused.Option.Foreground(p.Text)
		t.Focused.SelectedOption = t.Focused.SelectedOption.Foreground(p.OK)
		t.Focused.SelectedPrefix = lipgloss.NewStyle().Foreground(p.OK).SetString("✓ ")
		t.Focused.UnselectedPrefix = lipgloss.NewStyle().Foreground(p.Muted).SetString("• ")
		t.Focused.UnselectedOption = t.Focused.UnselectedOption.Foreground(p.Text)
		t.Focused.FocusedButton = t.Focused.FocusedButton.Foreground(p.BadgeText).Background(p.Accent).Bold(true)
		t.Focused.Next = t.Focused.FocusedButton
		t.Focused.BlurredButton = t.Focused.BlurredButton.Foreground(p.Muted).Background(p.Faint)
		t.Focused.TextInput.Cursor = t.Focused.TextInput.Cursor.Foreground(p.Accent)
		t.Focused.TextInput.Placeholder = t.Focused.TextInput.Placeholder.Foreground(p.Faint)
		t.Focused.TextInput.Prompt = t.Focused.TextInput.Prompt.Foreground(p.Accent)

		t.Blurred = t.Focused
		t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
		t.Blurred.Card = t.Blurred.Base
		t.Blurred.Title = t.Blurred.Title.Foreground(p.Muted).Bold(false)
		t.Blurred.NextIndicator = lipgloss.NewStyle()
		t.Blurred.PrevIndicator = lipgloss.NewStyle()

		t.Group.Title = t.Focused.Title.MarginBottom(1)
		t.Group.Description = t.Focused.Description
		return t
	}
}
