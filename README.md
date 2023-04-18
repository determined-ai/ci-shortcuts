# CI Shortcuts

This is a little go-based webserver that scrapes CircleCI build artifacts for a
few well-known URLs that somebody might want quick access to.

CircleCI's REST API is pretty crappy, so we do a lot of caching.  The caching
is persisted in SQLite, and we use bun to work with it.

## Server configuration

The server runs in GCP on the `dzshu-det-ci-server`.  There is an nginx
reverse-proxy in front of it.

## Running the server

On the VM, the server code is in `/root/ci-shortcuts`.  It is
configured so that you can run `sudo systemctl restart shortcuts` if you need
to.

## Deploying updates to the server

Follow these steps:

```bash
# ssh into vm
gcloud compute ssh --zone us-west2-b --ssh-flag=-A dzhu-det-ci-server

# switch accounts
sudo su

# pull updates
cd /root/ci-shortcuts
git pull origin main

# rebuild/reinstall
make install

## optional, if you modified the sqlite schema
# rm /var/cache/shortcuts/sqlite.db

# check on the shortcuts.service
systemctl status shortcuts.service
```
