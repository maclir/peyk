# peyk

Follow your aoe coding sessions from your phone: what each agent last said, whether its work is committed and pushed, and whether it needs you.

## Use

    make status           # print the board in the terminal
    make install-agent    # run the agent under launchd, with a menu bar icon to start, stop and restart it
    make restart-agent    # rebuild and restart it after a change

The phone app is the Cloud Run service. Sign in with Google once, then admit that account on the laptop:

    make owner EMAIL=you@example.com

To send tasks, replies, stops and pushes from the phone, pair its passkey once:

    make pair LABEL=pixel      # open the printed link on the phone, compare fingerprints
    make keys                  # list paired passkeys
    make unpair LABEL=pixel

## Provision

`make help` lists every target. Set `GCLOUD_ACCOUNT` in `.env` first; cloud targets refuse to run without it. Steps no script can do:

- Enable the Google sign-in provider in the Firebase console (Authentication → Sign-in method), and add the Cloud Run hostname under Authorized domains.
- Copy the web config from `firebase apps:create web` into `.env` as `FIREBASE_*`.
- Rerun `make harden` after any new Cloud Run address or custom domain, or the browser key refuses it.

## Develop

```sh
cp .env.example .env
make run       # http://localhost:8080, phone app shell without Firebase
make test      # the complete pre-commit and CI contract
make help
```
