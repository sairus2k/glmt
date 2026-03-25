# Re-recording the demo GIF

## Prerequisites

- [VHS](https://github.com/charmbracelet/vhs) (`brew install vhs`)
- Go 1.21+

## Steps

1. Build with demo mode:
   ```bash
   go build -o glmt ./cmd/glmt/
   ```

2. Record the GIF:
   ```bash
   vhs demo.tape
   ```

3. The output is `demo.gif` at the project root.

## Editing the demo

- **Mock data:** `internal/demo/client.go` — canned projects, MRs, and API responses
- **Tape script:** `demo.tape` — VHS commands that drive the TUI interaction
- **Timing:** Adjust `Sleep` values in the tape and delay durations in the demo client
- **Poll intervals:** Set in `cmd/glmt/demo.go` (currently 1s for both rebase and pipeline)

## Testing changes

Run `./glmt --demo` interactively to verify the TUI flow before recording with VHS.
