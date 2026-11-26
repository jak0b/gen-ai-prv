# gen-ai-prv

`gen-ai-prv` is a Go CLI and local web app that sends images, audio files, and
PDFs to OpenAI's [Content Provenance API](https://developers.openai.com/api/docs/guides/content-provenance)
and displays supported provenance signals (such as C2PA and SynthID), outcomes,
and available metadata. It does not analyze arbitrary text.

**This is not a general-purpose AI detector.** `not_detected` means only that
the API found no supported signal; it does not prove content is human-made or
not AI-generated.

## Requirements and quick start

Requires Go 1.22+ and an OpenAI API key with access to the Content Provenance
API.

```sh
go build -o gen-ai-prv .
export OPENAI_API_KEY="your-api-key"
./gen-ai-prv ./image.png
```

The CLI uses `OPENAI_API_KEY` first, then the OS keyring; if neither has a key,
it prompts without echoing and saves it to the keyring. The web server uses the
environment or keyring but does not prompt.

## CLI

```sh
./gen-ai-prv ./image.png ./audio.mp3 ./document.pdf
./gen-ai-prv -json ./document.pdf
```

Each file gets a separate API request. Default output shows a concise
detected/not-detected status (colored in terminals) and detailed results;
errors return a nonzero status. `-json` prints the API response for one file, or
an array of `{ "file": ..., "response": ... }` objects for multiple files.

## Web app

```sh
./gen-ai-prv serve
```

Open <http://127.0.0.1:8080>. Select or drag in files, choose which to check,
and view results as each file is checked individually. The HTML and assets are
embedded in the executable.

The default listener is `127.0.0.1:8080`. Configure the host/port and optional
TLS certificate/key with flags:

```sh
./gen-ai-prv serve --host 0.0.0.0 --port 9090
./gen-ai-prv serve --host example.com --port 8443 --tls-cert server.crt --tls-key server.key
```

Use `--host :` to listen on all interfaces (dual-stack where supported). Only
expose the server on a trusted network; use TLS beyond the local machine.
Options can also be set in `gen-ai-prv.yaml` or `.yml` in the current directory,
`~/.config/gen-ai-prv/`, or the home directory; select another file with
`--config`. Example:

```yaml
host: 127.0.0.1
port: 8443
tls-cert: server.crt
tls-key: server.key
```

Precedence is flags, `GEN_AI_PRV_HOST`, `GEN_AI_PRV_PORT`,
`GEN_AI_PRV_TLS_CERT`, `GEN_AI_PRV_TLS_KEY`, then config file. Deprecated
`--addr` remains supported. At startup, the server reports whether the API key
came from the environment or keyring.

## Supported files and privacy

Supported extensions: PNG, JPG/JPEG, WebP, GIF, MP3, Opus, AAC, FLAC, WAV, PCM,
and PDF. Inputs must be non-empty regular files up to 50 MiB; audio must be
60 seconds or shorter after decoding. Media type is inferred from the extension
(Opus uses `audio/ogg`); uploads use a generic ASCII filename.

File contents are uploaded to OpenAI for checking. The web server temporarily
stages each upload locally and removes it after the check. Review the API's
documentation and data controls before sending sensitive content.

## Container

Build with Podman or Docker:

```sh
podman build -f Containerfile .
```

The `scratch` image runs as non-root and has no OS keyring; pass the API key
through the environment.

## Development

```sh
go test ./...
go vet ./...
```
