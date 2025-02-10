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
		spotifyauth.WithScopes(scopes...))

	var cfg Config

	b, err := os.ReadFile(configFile)
	if err == nil {
		err = json.Unmarshal(b, &cfg)
		if err == nil {
			cfg.auth = auth
			return &cfg, nil
		}
	}

	fmt.Print("ClientID: ")
	stdin := bufio.NewScanner(os.Stdin)
	if !stdin.Scan() {
		return nil, fmt.Errorf("canceled")
	}
	clientID := stdin.Text()

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
	flag.BoolVar(&jsonout, "json", false, "output json")
	flag.BoolVar(&oneshot, "oneshot", false, "output once")

	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	client := cfg.NewClient(ctx)

	enc := json.NewEncoder(os.Stdout)
	artist := ""
	album := ""
	title := ""
	for {
		curr, err := client.PlayerCurrentlyPlaying(ctx)
		if err == nil {
			if curr != nil && curr.Item != nil {
				if artist != curr.Item.Artists[0].Name || album != curr.Item.Album.Name || curr.Item.Name != title {
					if jsonout {
						enc.Encode(payload{
							Album: curr.Item.Artists[0].Name,
							Title: curr.Item.Name,
						})
					} else {
						fmt.Printf("%s - %s\n", curr.Item.Artists[0].Name, curr.Item.Name)
					}
					if oneshot {
						break
					}
				}
				artist = curr.Item.Artists[0].Name
				album = curr.Item.Album.Name
				title = curr.Item.Name
			}
		} else {
			log.Println(err)
			if strings.Contains(err.Error(), "invalid_client") || strings.Contains(err.Error(), "The access token expired") {
				tok, err := client.Token()
				if err == nil {
					println("UPDATE", tok.Expiry.String())
					cfg.Token = tok
					client = cfg.NewClient(ctx)
					println("UPDATE")
					saveConfig(cfg)
				}
			} else {
				log.Println(err)
			}
		}
		time.Sleep(10 * time.Second)
	}
}
