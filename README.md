# Lilli

Lilli is a SMTP proxy. It transparently proxies SMTP sessions to an upstream SMTP server. Its main use case is to allow clients which only support TLSv1.0 or plain SMTP to speak with servers that require modern encryption.

## Features

* Transparently proxies a SMTP session to another SMTP server. (Without storing the mail in a queue. If the upstream server rejects the message, the client will receive that reject immediately. No bounce message is sent.)
* Convert between any combination of SMTP, SMTPs and SMTP+STARTTLS

## Installation

* Unpack `lilli-*.tar.gz,`
* As root, run `install.sh` from the unpacked directory
* Lilli is now installed, but will fail to start. It needs to be configured (see below).

## Configuration

An example config file is provided in `/opt/lilli/etc/lilli.conf.example`. Copy it to `/opt/lilli/etc/lilli.conf` and change it according to the comments in the file itself.

## Limitations

* The upstream server only sees the proxy's IP. This can cause trouble with spam-filtering and access control: If the upstream server blocks Lilli's IP or greylists it, no client can send any mail to this server via Lilli.
* No support for "advanced" SMTP features like SMTPUTF8. This is because when the client connects, Lilli doesn't know yet what upstream server will be used and what features it supports.

# Feature ideas

* Dynamic proxying to differnt servers based on username
* Multiple listeners with different configuration (port 25, 587, 465, ...)
* Send XCLIENT to upstream, if supported
* Auto-detect upstream TLS capabilities
* Merge with Willi to support MX and submission use cases

# Development

## Work on the code

```
$> go get   # get dependencies
... develop ...
$> go build # build for local testing
```

## Build a release

```
$> git tag -a v1.2.3 -m "v1.2.3"
$> make release # builds lilli-v1.2.3.tar.gz
$> git push --tags

Optional: Install release

$> make install TARGET_HOST=my.server.com
```
