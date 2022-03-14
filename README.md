# CI Shortcuts

This is a little go-based webserver that scrapes CircleCI build artifacts for a
few well-known URLs that somebody might want quick access to.

CircleCI's REST API is pretty crappy, so we do a lot of caching.  The caching
is persisted in SQLite, and we use bun to work with it.

## Server configuration

The server runs in GCP under the name `ci-shortcuts`.  The VM runs with:

 - No service account, so it has no access to GCP APIs
 - A dedicated VPN, so it can't reach any other Determined instances on
   internal IP addresses

## Running the server

On the VM, the server code is in `/root/ci-shortcuts`.  It is
configured so that you can run `sudo systemctl restart shortcuts` if you need
to.
