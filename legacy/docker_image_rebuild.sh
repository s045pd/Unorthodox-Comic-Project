#!/bin/bash
set -e

# Load environment variables from .env file if it exists
[ ! -f .env ] || export $(grep -v '^#' .env | xargs)

DOCKER_CON_NAME=se8
DOCKER_CON_NETWORK=se8
DOCKER_IM_NAME=se8

echo 'Building the image...'
docker build -t "$DOCKER_IM_NAME" ./

# Get the ID of the currently running docker container
DOCKER_ID=$(docker ps -a | grep "$DOCKER_CON_NAME" | awk '{print $1}')
echo "$DOCKER_ID"

# Check and try to remove the container if it exists
if [ -n "$DOCKER_ID" ]; then
    echo 'Removing the previous container:'
    docker stop "$DOCKER_ID"
    docker rm "$DOCKER_ID"
fi

echo 'Creating network...'
docker network create $DOCKER_CON_NETWORK || echo 'Network already exists.'

# Run a new Docker container
NEW_DOCKER_ID=$(docker run -d --name "$DOCKER_CON_NAME" --network "$DOCKER_CON_NETWORK" \
    -p 8000:8000 \
    -e USE_SQLITE=True \
    -e DJANGO_SUPERUSER_USERNAME=admin \
    -e DJANGO_SUPERUSER_PASSWORD=admin \
    -v "$(pwd)/vol/logs/:/var/log/server" \
    -v "$(pwd)/vol/media:/opt/server/vol/media" \
    "$DOCKER_IM_NAME")

echo 'Creating a new container: '"$NEW_DOCKER_ID"

docker ps | grep se8 | awk '{print $1}' | xargs docker logs -f
