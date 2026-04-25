# WillChain 🔒

**The world's first trustless digital will protocol built on Canopy Network.**

> Lock tokens. Set a timer. The chain enforces your legacy. No lawyers, no middlemen, pure mathematics.

## What is WillChain?

Every year billions in cryptocurrency is lost forever when people die with no inheritance plan. WillChain solves this with a dead man's switch built directly on a Canopy appchain.

## How It Works

1. **Create Will** — Lock tokens, name your beneficiary, set a block-height timer, leave a farewell message
2. **Stay Alive** — Reset the timer periodically to prove you are still here
3. **Beneficiary Claims** — If timer expires, beneficiary claims tokens automatically on-chain

## Transaction Types

- `create_will` — Lock tokens for a beneficiary with a block timer
- `reset_timer` — Owner proves liveness and extends the countdown
- `claim_will` — Beneficiary claims after timer expires
- `cancel_will` — Owner cancels and reclaims tokens anytime

## Tech Stack

- **Blockchain:** Canopy Network (Go plugin, NestBFT consensus)
- **Plugin:** Go — custom transaction types via Canopy plugin interface
- **Frontend:** Pure HTML/CSS/JS — no framework
- **RPC:** Canopy HTTP RPC (port 50002)

## Run Locally

```bash
# Clone repo
git clone https://github.com/Fidelisdigital/willchain.git
cd willchain

# Build plugin
cd plugin/go
go build -o go-plugin .

# Configure canopy
# Set "plugin": "go" in ~/.canopy/config.json

# Start chain
canopy start

# Open frontend
open willchain.html
```

## One-Line Pitch

> WillChain is the first trustless on-chain digital will — permanent, tamper-proof token inheritance enforced by the Canopy blockchain.

## Built For

Canopy Network Vibe Code Contest 2026

## License

MIT
