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
	"time"

	cv "github.com/nirasan/go-oauth-pkce-code-verifier"

	"github.com/zmb3/spotify"
	"golang.org/x/oauth2"
)

type Config struct {
	ClientID string        `json:"client_id"`
	Token    *oauth2.Token `json:"token"`

	auth spotify.Authenticator
}

func (c *Config) NewClient() spotify.Client {
	return c.auth.NewClient(c.Token)
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
		spotify.ScopeUserReadPrivate,
		spotify.ScopeUserReadCurrentlyPlaying,
		spotify.ScopeUserReadPlaybackState,
		spotify.ScopeUserModifyPlaybackState,
		spotify.ScopeUserLibraryModify,
		spotify.ScopeUserLibraryRead,
	}
	auth := spotify.NewAuthenticator("http://localhost:3000/callback", scopes...)

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

			tok, err := auth.TokenWithOpts(state, r,
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

	url := auth.AuthURLWithOpts(state,
		oauth2.SetAuthURLParam("client_id", clientID),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
	)

	err = openBrowser(url)
	if err != nil {
		return nil, err
	}

	cfg.auth = auth
	cfg.ClientID = clientID
	cfg.Token = <-tokenCh

	err = server.Shutdown(context.Background())
	if err != nil {
		return nil, err
	}

	b, err = json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	err = os.MkdirAll(configDir, 0700)
	if err != nil {
		return nil, err
	}

	err = os.WriteFile(configFile, b, 0644)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

func main() {
	var j bool
	flag.BoolVar(&j, "json", false, "output json")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}

	client := cfg.NewClient()

	enc := json.NewEncoder(os.Stdout)
	artist := ""
	album := ""
	title := ""
	for {
		curr, err := client.PlayerCurrentlyPlaying()
		if err == nil {
			if curr != nil && curr.Item != nil {
				if artist != curr.Item.Artists[0].Name || album != curr.Item.Album.Name || curr.Item.Name != title {
					if j {
						enc.Encode(payload{
							Album: curr.Item.Artists[0].Name,
							Title: curr.Item.Name,
						})
					} else {
						fmt.Printf("%s - %s\n", curr.Item.Artists[0].Name, curr.Item.Name)
					}
				}
				artist = curr.Item.Artists[0].Name
				album = curr.Item.Album.Name
				title = curr.Item.Name
			}
		} else {
			log.Println(err)
		}
		time.Sleep(10 * time.Second)
	}
}
