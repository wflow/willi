#!/bin/bash
if [ $UID != 0 ]; then
  echo "Installation must be run as root"
  exit 1
fi

MY_DIR=`dirname $0`
APP_DIR=/opt/willi
ETC_DIR=/opt/willi/etc

# User setup
adduser --system willi --no-create-home
mkdir -p $ETC_DIR && chown -R willi $ETC_DIR

# Stop existing service
systemctl stop willi.service

# Files
cp $MY_DIR/willi $APP_DIR
cp $MY_DIR/willi.service $APP_DIR
cp -r $MY_DIR/*.example $ETC_DIR

chown -R willi $ETC_DIR/*.example
chmod 0600 $ETC_DIR/*.example

# Service setup
cp $APP_DIR/willi.service /etc/systemd/system
systemctl daemon-reload
systemctl enable willi.service
systemctl start willi.service
