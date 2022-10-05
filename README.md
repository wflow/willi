# smtp-proxy

Smtp-proxy transparently proxies SMTP sessions to an upstream SMTP server. The upstream server is selected based on the recipients (`RCPT TO`) of the incoming mail.

## Installation

* Unpack smtp-proxy-*.tar.gz,
* As root, run `install.sh` from the unpacked directory
* smtp-proxy is now installed, but will fail to start. It needs to be configured (see below)

## Configuration

An example config file is provided in `/opt/smtp-proxy/etc/smtp-proxy.conf.example`. Copy it to `/opt/smtp-proxy/etc/smtp-proxy.conf`.

## Limitations

If a client specifies multiple `RCPT TO` headers, only the first is used to select an upstream server. It will receive the complete SMTP session, including all `RCPT TO` headers.

# Development

## Work on the code

```
$> go get   # get dependencies
... develop ...
$> go build # build for local testing
```

## Build a release

```
$> git tag -A v1.2.3 -m "v1.2.3"
$> make release # builds smtp-proxy-v1.2.3.tar.gz
$> git push --tags

Optional: Install release

$> make install TARGET_HOST=my.server.com
```
