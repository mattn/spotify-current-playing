package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	cv "github.com/nirasan/go-oauth-pkce-code-verifier"

	"github.com/zmb3/spotify/v2"
	"github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"
)

type Config struct {
	ClientID string        `json:"client_id"`
	Token    *oauth2.Token `json:"token"`

	auth       *spotifyauth.Authenticator
	configFile string
}

func (c *Config) NewClient(ctx context.Context) *spotify.Client {
	return spotify.New(oauth2.NewClient(ctx, oauth2.StaticTokenSource(c.Token)))
}

type payload struct {
	Artist string `json:"artist"`
	Album  string `json:"album"`
	Title  string `json:"title"`
}

func (p *payload) Same(c *payload) bool {
	return p.Artist != c.Artist || p.Album != c.Album || p.Title != c.Title
}

func (p *payload) JSON() string {
	b, _ := json.Marshal(p)
	return string(b)
}

func (p *payload) String() string {
	return fmt.Sprintf("%s - %s", p.Artist, p.Title)
}

func openBrowser(url string) error {
	var err error

	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("unsupported platform")
	}
	return err
}

func loadConfig() (*Config, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	configDir = filepath.Join(configDir, "spotify-current-playing")
	configFile := filepath.Join(configDir, "config.json")

	scopes := []string{
		spotifyauth.ScopeUserReadPrivate,
		spotifyauth.ScopeUserReadCurrentlyPlaying,
		spotifyauth.ScopeUserReadPlaybackState,
		spotifyauth.ScopeUserModifyPlaybackState,
		spotifyauth.ScopeUserLibraryModify,
		spotifyauth.ScopeUserLibraryRead,
	}
	auth := spotifyauth.New(
		spotifyauth.WithRedirectURL("http://localhost:3000/callback"),
		spotifyauth.WithScopes(scopes...),
	)

	var cfg Config

	b, err := os.ReadFile(configFile)
	if err == nil {
		err = json.Unmarshal(b, &cfg)
		if err == nil && cfg.Token.Valid() {
			auth := spotifyauth.New(
				spotifyauth.WithClientID(cfg.ClientID),
				spotifyauth.WithScopes(scopes...),
			)
			cfg.auth = auth

			cfg.configFile = configFile
			return &cfg, nil
		}
	}

	var clientID string
	if cfg.ClientID != "" {
		clientID = cfg.ClientID
	} else {
		fmt.Print("ClientID: ")
		stdin := bufio.NewScanner(os.Stdin)
		if !stdin.Scan() {
			return nil, fmt.Errorf("canceled")
		}
		clientID = stdin.Text()
	}

	cvInstance, err := cv.CreateCodeVerifier()
	if err != nil {
		log.Fatal(err)
	}
	codeVerifier := cvInstance.String()
	codeChallenge := cvInstance.CodeChallengeS256()
	state := "abc123"

	tokenCh := make(chan *oauth2.Token)

	server := &http.Server{
		Addr: ":3000",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/callback" {
				http.NotFound(w, r)
				return
			}

			if r.URL.Query().Get("code") == "" {
				fmt.Fprintf(w, "Missing Parameters!")
				return
			}

			tok, err := auth.Token(r.Context(), state, r,
				oauth2.SetAuthURLParam("client_id", clientID),
				oauth2.SetAuthURLParam("code_verifier", codeVerifier))
			if err != nil {
				http.Error(w, "Couldn't get token", http.StatusForbidden)
				log.Fatalf("Failed while creating token: %v", err)
			}
			if st := r.FormValue("state"); st != state {
				http.NotFound(w, r)
				log.Fatalf("Failed while checking state, mismatch: %s != %s\n", st, state)
			}
			w.Header().Set("content-type", "text/html")
			fmt.Fprintf(w, `<script>window.open("about:blank","_self").close();</script>`)
			tokenCh <- tok
		}),
	}
	go server.ListenAndServe()

	url := auth.AuthURL(state,
		oauth2.SetAuthURLParam("client_id", clientID),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
	)

	err = openBrowser(url)
	if err != nil {
		return nil, err
	}

	cfg = Config{
		ClientID:   clientID,
		Token:      <-tokenCh,
		auth:       auth,
		configFile: configFile,
	}

	err = server.Shutdown(context.Background())
	if err != nil {
		return nil, err
	}

	err = saveConfig(&cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

func saveConfig(cfg *Config) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(cfg.configFile), 0700)
	if err != nil {
		return err
	}

	err = os.WriteFile(cfg.configFile, b, 0644)
	if err != nil {
		return err
	}

	return nil
}

func main() {
	var jsonout bool
	var oneshot bool
	var verbose bool
	flag.BoolVar(&jsonout, "json", false, "output json")
	flag.BoolVar(&oneshot, "oneshot", false, "output once")
	flag.BoolVar(&verbose, "verbose", false, "verbose")

	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	client := cfg.NewClient(ctx)

	var prev payload
	for {
		curr, err := client.PlayerCurrentlyPlaying(ctx)
		if err == nil {
			if curr.Playing {
				curr := payload{
					Artist: curr.Item.Artists[0].Name,
					Album:  curr.Item.Album.Name,
					Title:  curr.Item.Name,
				}
				if verbose {
					log.Print(curr.JSON())
				}
				if !prev.Same(&curr) {
					if jsonout {
						fmt.Println(curr.JSON())
					} else {
						fmt.Println(curr.String())
					}
					if oneshot {
						break
					}
				}
				prev = curr
			}
		} else {
			if strings.Contains(err.Error(), "The access token expired") {
				refresh, err := client.Token()
				if err == nil {
					cfg.Token = refresh
					saveConfig(cfg)
				}
			} else {
				log.Println(err)
			}
		}
		time.Sleep(10 * time.Second)
	}
}
