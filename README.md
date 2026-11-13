# Ordarr

Self-hosted watch-order management for movies and individual TV episodes.

Build collections in your preferred order, keep deliberate repeats, and connect your Plex server.

**Early development:** collection editing and Plex connections are available. Media matching, playlist sync, and import/export are still being built.

## Quick start

```sh
cp .env.example .env
docker compose up -d --build --wait
```

Open port **8080** on the host and create your administrator account. Access is local-only by default.

## Documentation

Installation, Plex setup, configuration, backups, and development guides are in the **[wiki](https://github.com/trentnbauer/Ordarr/wiki)**.

See the [to-do list](TODO.md) for current progress.
