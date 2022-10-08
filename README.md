# smtp-proxy

Smtp-proxy transparently proxies SMTP sessions to an upstream SMTP server. The upstream server is selected based on the recipients (`RCPT TO`) of the incoming mail. Its main usage is as a proxy for MX servers.

## Features

* Transparently proxy an SMTP session to another SMTP server. (Without storing the mail in a queue. If upstream server rejects the message, the client will receive that reject immediately. No bounce message is sent.)
* Select upstream server based on mail recipient (RCPT TO).
* Read mapping from recipient to upstream server from MySQL database or CSV file
* Flexible number and ordering of mappings
* STARTTLS support in connection to clients
* Always use STARTTLS in connection to upstream server (if upstream server supports it)
* Forward real client IP via XCLIENT, if upstream server supports it.

## Installation

* Unpack smtp-proxy-*.tar.gz,
* As root, run `install.sh` from the unpacked directory
* smtp-proxy is now installed, but will fail to start. It needs to be configured (see below).

## Configuration

An example config file is provided in `/opt/smtp-proxy/etc/smtp-proxy.conf.example`. Copy it to `/opt/smtp-proxy/etc/smtp-proxy.conf`.
Now change it by following the comments in the file itself.

## Limitations

If a client specifies multiple `RCPT TO` headers, only the first is used to select an upstream server. It will receive the complete SMTP session, including all `RCPT TO` headers. If the upstream server does not accept mail for all recipients, it will reject the mail.

No support for "advanced" SMTP features like SMTPUTF8. This is because when the client connects, smtp-proxy doesn't know yet, what upstream server will be used and what feature set it supports.

No support for authentication. This is by design, as smtp-proxy is primarily meant to be used for incoming mail.

Breaks greylisting on upstream servers unless they support XCLIENT. Without XCLIENT the upstream server only sees the proxy's IP.

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
