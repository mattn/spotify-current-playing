# spotify-current-playing

print current playing on spotify

## Usage

```
./spotify-current-playing
```

## Requirements

Spotify Client ID

https://developer.spotify.com/

1. Go to dashboard
2. Create app
3. Set App name as: nostr-nowplaying
4. Set App description as: Update #nowplaying NIP-38 status (kind 30315) for Spotify
5. Set Redirect URIs as: http://localhost:3000/callback
6. Check Web API and Web Playback SDK
7. Save and copy the Client ID

## Installation

```
go install github.com/mattn/spotify-current-playing
```

## License

MIT

## Author

Yasuhiro Matsumoto (a.k.a. mattn)
