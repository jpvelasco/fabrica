# fabrica-cli

npm distribution of [Fabrica](https://github.com/jpvelasco/fabrica) — a Go CLI + infrastructure-as-code framework that provisions and manages game-studio cloud infrastructure on AWS (Perforce Helix Core, Unreal Horde, CI/CD, GameLift deployment, cloud workstations).

## Install

```bash
npm install -g fabrica-cli
# or run without installing:
npx fabrica-cli --help
```

This package is a thin launcher: on install it downloads the matching prebuilt `fabrica` binary for your platform from the [GitHub Releases](https://github.com/jpvelasco/fabrica/releases) and verifies its SHA-256 checksum. Supported: linux/macOS/windows on amd64, and linux/macOS on arm64.

Set `FABRICA_SKIP_AUTO_DOWNLOAD=1` to manage the binary yourself (air-gapped setups).

## Alternative install

```bash
go install github.com/jpvelasco/fabrica@latest
```

See the [main README](https://github.com/jpvelasco/fabrica#readme) for usage.
