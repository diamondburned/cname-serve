# cname-serve

DNS server that only serves CNAME records. Unknown queries are delegated to
a fallback DNS server.

## Intended Use-case

cname-serve was made to be used alongside Tailscale Split DNS. For example, if
you have `ha.d14.place`, you can use cname-serve to serve a CNAME record
pointing to an internal Tailscale hostname only when you are inside the Tailnet
itself, otherwise delegating to public DNS records.

## Example Configuration

See [config.example.toml](config.example.toml) for an example configuration.
Run it as `cname-serve -c config.toml`.
