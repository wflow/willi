#!/bin/bash
if [ $UID != 0 ]; then
  echo "Installation must be run as root"
  exit 1
fi

MY_DIR=`dirname $0`
APP_DIR=/opt/lilli
ETC_DIR=/opt/lilli/etc

# User setup
adduser --system lilli --no-create-home
mkdir -p $ETC_DIR && chown -R lilli $ETC_DIR

# Stop existing service
systemctl stop lilli.service

# Files
cp $MY_DIR/lilli $APP_DIR
cp $MY_DIR/lilli.service $APP_DIR
cp -r $MY_DIR/*.example $ETC_DIR

chown -R lilli $ETC_DIR/*.example
chmod 0600 $ETC_DIR/*.example

# Service setup
cp $APP_DIR/lilli.service /etc/systemd/system
systemctl daemon-reload
systemctl enable lilli.service
systemctl start lilli.service
