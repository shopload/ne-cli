package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/shopload/ne-cli/internal/config"
)

func openStore() (*config.Store, error) {
	s, err := config.Open(opts.profile)
	if err != nil {
		return nil, &cliError{Type: "config", Message: err.Error(), exit: exitUsage}
	}
	return s, nil
}

func mask(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "…" + s[len(s)-2:]
}

func newConfigCmd() *cobra.Command {
	c := &cobra.Command{Use: "config", Short: "Set up the Next Engine app credentials", GroupID: "core"}

	var clientID, clientSecret, redirectURI, apiHost, baseHost string
	var secretStdin bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: "Save client_id, client_secret and redirect_uri for a profile",
		Long: `Save the credentials of your Next Engine app (アプリを作る → 編集 → API タブ).

Missing values are prompted for when run in a terminal. To keep the secret out
of shell history and process lists, pipe it with --client-secret-stdin or set
NE_CLIENT_SECRET instead of using --client-secret.`,
		Example: `  ne-cli config init
  echo "$SECRET" | ne-cli config init --client-id abc --client-secret-stdin --redirect-uri https://localhost:8443/callback`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			if secretStdin {
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				clientSecret = strings.TrimSpace(line)
			}
			tty := term.IsTerminal(int(os.Stdin.Fd())) && !secretStdin
			if clientID == "" && store.Profile.ClientID == "" && tty {
				clientID = prompt("client_id: ", false)
			}
			if clientSecret == "" && store.Profile.ClientSecret == "" && tty {
				clientSecret = prompt("client_secret: ", true)
			}
			if redirectURI == "" && store.Profile.RedirectURI == "" && tty {
				redirectURI = prompt("redirect_uri (as registered in the app; empty to skip): ", false)
			}
			if err := store.Update(func(p *config.Profile) {
				set(&p.ClientID, clientID)
				set(&p.ClientSecret, clientSecret)
				set(&p.RedirectURI, redirectURI)
				set(&p.APIHost, apiHost)
				set(&p.BaseHost, baseHost)
			}); err != nil {
				return err
			}
			if store.Profile.ClientID == "" || store.Profile.ClientSecret == "" {
				return &cliError{Type: "config", Message: "client_id and client_secret are both required",
					Hint: "pass --client-id and --client-secret-stdin, or set NE_CLIENT_ID / NE_CLIENT_SECRET", exit: exitUsage}
			}
			return showConfig(cmd, store, "ne-cli auth login")
		},
	}
	f := initCmd.Flags()
	f.StringVar(&clientID, "client-id", "", "app client ID")
	f.StringVar(&clientSecret, "client-secret", "", "app client secret (prefer --client-secret-stdin)")
	f.BoolVar(&secretStdin, "client-secret-stdin", false, "read the client secret from stdin")
	f.StringVar(&redirectURI, "redirect-uri", "", "Redirect URI registered in the app")
	f.StringVar(&apiHost, "api-host", "", "override the API host (default "+config.DefaultAPIHost+")")
	f.StringVar(&baseHost, "base-host", "", "override the auth host (default "+config.DefaultBaseHost+")")

	showCmd := &cobra.Command{
		Use: "show", Short: "Show the active profile (secrets masked)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			return showConfig(cmd, store, "")
		},
	}
	c.AddCommand(initCmd, showCmd)
	return c
}

func showConfig(cmd *cobra.Command, store *config.Store, next string) error {
	p := store.Profile
	out := map[string]any{
		"profile": store.Name, "config_file": store.Path, "profiles": store.Profiles(),
		"client_id": p.ClientID, "client_secret": mask(p.ClientSecret), "redirect_uri": p.RedirectURI,
		"api_host": p.APIHost, "base_host": p.BaseHost,
	}
	if next != "" {
		out["next"] = next
	}
	return emit(cmd, out, nil)
}

func set(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func prompt(label string, secret bool) string {
	fmt.Fprint(os.Stderr, label)
	if secret {
		b, _ := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b))
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line)
}
