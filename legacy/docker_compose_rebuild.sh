#!/bin/bash
set -e
docker ps -a | grep se8 | awk '{print $1}' | xargs docker rm -f
docker images | grep se8 | awk '{print $3}' | xargs docker rmi -f

docker-compose up --force-recreate -d --build
sleep 5
cid="$(docker ps | grep se8-server | awk '{print $1}')"
echo "Container ID: $cid"
docker exec -it "$cid" tail -f vol/logs/*
