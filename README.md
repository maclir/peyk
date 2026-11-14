# peyk

Follow your aoe coding sessions from your phone: what each agent last said, whether its work is committed and pushed, and whether it needs you.

## Use

    make status    # print the board in the terminal
    make serve     # publish to the phone until Ctrl-C; run it in the relayServer aoe session

The phone app is the Cloud Run service. Sign in with Google once, then admit that account on the laptop:

    make owner EMAIL=you@example.com

## How it works

`docs/architecture.md` describes the laptop agent, the phone app and the Firestore rules between them. `docs/gcp.md` covers the cloud setup.

## Develop

```sh
cp .env.example .env
make run       # http://localhost:8080, phone app shell without Firebase
make test      # the complete pre-commit and CI contract
make help
```
