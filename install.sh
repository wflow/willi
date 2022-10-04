#!/bin/bash
if [ $UID != 0 ]; then
  echo "Installation must be run as root"
  exit 1
fi

MY_DIR=`dirname $0`
APP_DIR=/opt/smtp-proxy
ETC_DIR=/opt/smtp-proxy/etc

# User setup
adduser --system smtp-proxy --no-create-home
mkdir -p $ETC_DIR && chown -R smtp-proxy $ETC_DIR

# Stop existing service
systemctl stop smtp-proxy.service

# Files
cp $MY_DIR/smtp-proxy $APP_DIR
cp $MY_DIR/smtp-proxy.service $APP_DIR
cp -r $MY_DIR/*.example $ETC_DIR

chown -R smtp-proxy $ETC_DIR/*.example
chmod 0600 $ETC_DIR/*.example

# Service setup
cp $APP_DIR/smtp-proxy.service /etc/systemd/system
systemctl daemon-reload
systemctl enable smtp-proxy.service
systemctl start smtp-proxy.service
