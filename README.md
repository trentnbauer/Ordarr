# Ordarr

Self-hosted watch-order management for movies and individual TV episodes.

Build collections in your preferred order, keep deliberate repeats, and connect your Plex server.

**Development build:** includes Plex matching and playlist sync, YAML/JSON imports and subscriptions, and optional Sonarr/Radarr requests. Live service compatibility is still being verified.

## Quick start

```sh
cp .env.example .env
docker compose up -d --build --wait
```

Open port **8080** on the host and create your administrator account. Access is local-only by default.

## Documentation

Installation, Plex setup, configuration, backups, and development guides are in the **[wiki](https://github.com/trentnbauer/Ordarr/wiki)**.

See the [to-do list](TODO.md) for current progress.
