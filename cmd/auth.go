package cmd

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shopload/ne-cli/internal/client"
	"github.com/shopload/ne-cli/internal/config"
	"github.com/shopload/ne-cli/internal/registry"
)

var jst = time.FixedZone("JST", 9*60*60)

func newAuthCmd() *cobra.Command {
	c := &cobra.Command{Use: "auth", Short: "Log in to Next Engine and manage tokens", GroupID: "core"}
	c.AddCommand(newLoginCmd(), newStatusCmd(), newRefreshCmd(), newImportCmd(), newLogoutCmd())
	return c
}

func newLoginCmd() *cobra.Command {
	var uid, state, callbackURL string
	var noWait, noBrowser bool
	var waitFor time.Duration
	c := &cobra.Command{
		Use:   "login",
		Short: "Log in through the browser and store access/refresh tokens",
		Long: `Runs the Next Engine login flow:

  1. open  <base host>/users/sign_in/?client_id=…&redirect_uri=…  in a browser
  2. after login, Next Engine redirects to redirect_uri with ?uid=…&state=…
  3. uid and state are exchanged for tokens at /api_neauth

When redirect_uri points at localhost (http or https, with a port), ne-cli
listens there and finishes by itself; https uses a throwaway self-signed
certificate, so the browser shows a warning once.

Otherwise — or with --no-wait, which is what an AI agent should use — the
login URL is printed and the command returns. After logging in, pass the URL
you were redirected to with --callback-url (state is valid for 5 minutes).`,
		Example: `  ne-cli auth login
  ne-cli auth login --no-wait
  ne-cli auth login --callback-url 'https://example.com/cb?uid=…&state=…'`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			p := store.Profile
			if p.ClientID == "" || p.ClientSecret == "" {
				return &cliError{Type: "config", Message: "client_id / client_secret are not configured",
					Hint: "run `ne-cli config init`, or set NE_CLIENT_ID and NE_CLIENT_SECRET", exit: exitAuth}
			}
			if callbackURL != "" {
				u, err := url.Parse(callbackURL)
				if err != nil {
					return usageErr("--callback-url is not a URL: %v", err)
				}
				uid, state = u.Query().Get("uid"), u.Query().Get("state")
				if uid == "" || state == "" {
					return usageErr("--callback-url has no uid/state query parameters")
				}
			}
			if uid != "" || state != "" {
				if uid == "" || state == "" {
					return usageErr("--uid and --state must be given together")
				}
				return exchange(cmd, store, uid, state)
			}

			q := url.Values{"client_id": {p.ClientID}}
			if p.RedirectURI != "" {
				q.Set("redirect_uri", p.RedirectURI)
			}
			loginURL := strings.TrimRight(p.BaseHost, "/") + "/users/sign_in/?" + q.Encode()
			local := localRedirect(p.RedirectURI)
			if noWait || local == nil || opts.dryRun {
				return emit(cmd, map[string]any{
					"action_required": "open_url", "url": loginURL,
					"next": "log in at the URL, then run: ne-cli auth login --callback-url '<the URL you were redirected to>'",
					"note": "state expires 5 minutes after the redirect",
				}, nil)
			}
			got, err := listenForCallback(cmd.Context(), local, loginURL, !noBrowser, waitFor)
			if err != nil {
				return err
			}
			return exchange(cmd, store, got[0], got[1])
		},
	}
	f := c.Flags()
	f.BoolVar(&noWait, "no-wait", false, "print the login URL and return instead of waiting for the redirect")
	f.BoolVar(&noBrowser, "no-browser", false, "do not try to open the browser")
	f.StringVar(&callbackURL, "callback-url", "", "the redirected URL containing uid and state")
	f.StringVar(&uid, "uid", "", "uid from the redirect")
	f.StringVar(&state, "state", "", "state from the redirect")
	f.DurationVar(&waitFor, "login-timeout", 5*time.Minute, "how long to wait for the browser redirect")
	return c
}

// localRedirect returns the redirect URI when ne-cli can listen on it.
func localRedirect(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil || u.Port() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil
	}
	if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" {
		return nil
	}
	return u
}

func listenForCallback(ctx context.Context, redirect *url.URL, loginURL string, openBrowser bool, wait time.Duration) ([2]string, error) {
	var none [2]string
	ln, err := net.Listen("tcp", "127.0.0.1:"+redirect.Port())
	if err != nil {
		return none, &cliError{Type: "auth", Message: "cannot listen on " + redirect.Host + ": " + err.Error(),
			Hint: "free the port, or use `ne-cli auth login --no-wait`", exit: exitAuth}
	}
	if redirect.Scheme == "https" {
		cert, err := selfSigned()
		if err != nil {
			return none, err
		}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}})
	}
	result := make(chan [2]string, 1)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, state := r.URL.Query().Get("uid"), r.URL.Query().Get("state")
		if uid == "" || state == "" {
			http.Error(w, "waiting for the Next Engine redirect (no uid/state in this request)", http.StatusBadRequest)
			return
		}
		fmt.Fprintln(w, "ne-cli: login received. You can close this tab.")
		select {
		case result <- [2]string{uid, state}:
		default:
		}
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	fmt.Fprintln(os.Stderr, "Open this URL to log in to Next Engine:\n\n  "+loginURL+"\n\nWaiting for the redirect to "+redirect.String()+" …")
	if openBrowser {
		openURL(loginURL)
	}
	select {
	case got := <-result:
		return got, nil
	case <-ctx.Done():
		return none, ctx.Err()
	case <-time.After(wait):
		return none, &cliError{Type: "auth", Message: "timed out waiting for the login redirect",
			Hint: "retry, or use --no-wait and --callback-url", exit: exitAuth}
	}
}

func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "ne-cli login callback"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

func openURL(u string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", u)
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		c = exec.Command("xdg-open", u)
	}
	_ = c.Start()
}

// exchange trades uid/state for tokens and stores them.
func exchange(cmd *cobra.Command, store *config.Store, uid, state string) error {
	p := store.Profile
	params := map[string]string{"uid": uid, "state": state, "client_id": p.ClientID, "client_secret": p.ClientSecret}
	if opts.dryRun {
		body, err := send(cmd.Context(), requestFor(registry.Find("neauth.access_token.get"), params))
		if err != nil {
			return err
		}
		return emit(cmd, body, nil)
	}
	body, err := client.New(opts.timeout).Post(cmd.Context(), p.APIHost, "/api_neauth", params)
	if err != nil {
		ce := asCLIError(mapAPIError(err))
		if ce.Type == "api_error" {
			ce.Type, ce.exit = "auth", exitAuth
		}
		return ce
	}
	str := func(k string) string { s, _ := body[k].(string); return s }
	if str("access_token") == "" {
		return &cliError{Type: "auth", Message: "the token response contained no access_token", exit: exitAuth}
	}
	if err := store.Update(func(p *config.Profile) {
		p.AccessToken, p.AccessTokenEndDate = str("access_token"), str("access_token_end_date")
		p.RefreshToken, p.RefreshTokenEndDate = str("refresh_token"), str("refresh_token_end_date")
		p.RotatedFrom = ""
		p.CompanyName, p.CompanyNEID, p.UserName = str("company_name"), str("company_ne_id"), str("pic_name")
	}); err != nil {
		return err
	}
	return emit(cmd, statusOf(store), nil)
}

func statusOf(store *config.Store) map[string]any {
	p := store.Profile
	out := map[string]any{
		"profile": store.Name, "configured": p.ClientID != "" && p.ClientSecret != "",
		"logged_in": p.AccessToken != "", "token_source": "config",
	}
	if store.TokensFromEnv {
		out["token_source"] = "env"
	}
	if p.CompanyName != "" && !store.TokensFromEnv {
		out["company_name"], out["company_ne_id"], out["user_name"] = p.CompanyName, p.CompanyNEID, p.UserName
	}
	if p.AccessTokenEndDate != "" {
		out["access_token_end_date"] = p.AccessTokenEndDate
		out["access_token_expired"] = expired(p.AccessTokenEndDate)
	}
	if p.RefreshTokenEndDate != "" {
		out["refresh_token_end_date"] = p.RefreshTokenEndDate
		out["refresh_token_expired"] = expired(p.RefreshTokenEndDate)
		if expired(p.RefreshTokenEndDate) {
			out["logged_in"] = false
			out["hint"] = "the refresh token expired; run `ne-cli auth login`"
		}
	}
	return out
}

// expired reports whether a "YYYY-MM-DD hh:mm:ss" JST timestamp has passed.
func expired(s string) bool {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, jst)
	return err == nil && time.Now().After(t)
}

func newStatusCmd() *cobra.Command {
	var verify bool
	c := &cobra.Command{
		Use: "status", Short: "Show whether the profile is logged in and when tokens expire", Args: cobra.NoArgs,
		Long: `Shows the stored login state without printing any token.

The access token lasts 1 day and the refresh token 3 days; both are renewed
automatically whenever an API call is made with a valid refresh token. With
--verify the tokens are checked against the API.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			if verify {
				body, err := send(cmd.Context(), requestFor(registry.Find("login.user.info"), nil))
				if err != nil {
					return err
				}
				out := statusOf(store)
				out["verified"] = true
				if rows, _ := body["data"].([]any); len(rows) > 0 {
					out["user"] = rows[0]
				}
				return emit(cmd, out, nil)
			}
			return emit(cmd, statusOf(store), nil)
		},
	}
	c.Flags().BoolVar(&verify, "verify", false, "call the API to confirm the tokens work")
	return c
}

func newRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use: "refresh", Short: "Make one cheap API call so expired tokens are renewed and saved", Args: cobra.NoArgs,
		Long: `Tokens cannot be refreshed on their own: Next Engine renews them as a side
effect of any API call made with a valid refresh token. This command makes a
cheap call (login user info) and stores the result. Run it at least every
3 days (e.g. from cron) to keep an unattended profile logged in.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			if _, err := send(cmd.Context(), requestFor(registry.Find("login.user.info"), nil)); err != nil {
				return err
			}
			if opts.dryRun {
				return emit(cmd, map[string]any{"dry_run": true}, nil)
			}
			return emit(cmd, statusOf(store), nil)
		},
	}
}

func newImportCmd() *cobra.Command {
	return &cobra.Command{
		Use: "import", Short: "Store tokens obtained elsewhere (JSON on stdin)", Args: cobra.NoArgs,
		Long: `Reads {"access_token":"…","refresh_token":"…"} from stdin and stores it in the
profile, so tokens issued by another system can be used without the browser
flow. Tokens are read from stdin so they do not end up in shell history.`,
		Example: `  cat tokens.json | ne-cli auth import`,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			var in struct {
				AccessToken         string `json:"access_token"`
				RefreshToken        string `json:"refresh_token"`
				AccessTokenEndDate  string `json:"access_token_end_date"`
				RefreshTokenEndDate string `json:"refresh_token_end_date"`
			}
			if err := json.NewDecoder(cmd.InOrStdin()).Decode(&in); err != nil || in.AccessToken == "" {
				return usageErr(`expected JSON on stdin: {"access_token":"…","refresh_token":"…"}`)
			}
			if err := store.Update(func(p *config.Profile) {
				p.AccessToken, p.AccessTokenEndDate = in.AccessToken, in.AccessTokenEndDate
				p.RefreshToken, p.RefreshTokenEndDate = in.RefreshToken, in.RefreshTokenEndDate
				p.RotatedFrom, p.CompanyName, p.CompanyNEID, p.UserName = "", "", "", ""
			}); err != nil {
				return err
			}
			return emit(cmd, statusOf(store), nil)
		},
	}
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use: "logout", Short: "Delete the stored tokens of the profile", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openStore()
			if err != nil {
				return err
			}
			if err := store.Update(func(p *config.Profile) {
				p.AccessToken, p.AccessTokenEndDate, p.RefreshToken, p.RefreshTokenEndDate = "", "", "", ""
				p.RotatedFrom, p.CompanyName, p.CompanyNEID, p.UserName = "", "", "", ""
			}); err != nil {
				return err
			}
			return emit(cmd, map[string]any{"profile": store.Name, "logged_in": false}, nil)
		},
	}
}
