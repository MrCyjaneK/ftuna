#!/bin/bash

cd "$(dirname "$0")"

ftuna="go run $PWD/cmd/ftuna"

source .env

export S3_ENDPOINT
export S3_ACCESS_KEY_ID
export S3_SECRET_ACCESS_KEY
export S3_BUCKET_NAME
export S3_REGION
export FTUNA_RECIPES
export FTUNA_RATINGS
export FTUNA_OSS_GPG_PRIVATE_KEY

iter=0
#    echo '> ftuna 5.1.0.11 all'
#    $ftuna 5.1.0.11 all
#    exit $?
# while true; do
#    sleep 600
#    echo "Next run in 10 minutes"
    iter=$((iter + 1))
    echo '> ftuna 5.1.0.11 all'
    pushd "$FTUNA_RECIPES"
        git pull
    popd
    echo '> ftuna 5.1.0.11 all'
    $ftuna 5.1.0.11 all
    echo "> ftuna scan $FTUNA_RATINGS"
    $ftuna scan "$FTUNA_RATINGS"
#    if (( iter % 6 == 0 )); then
        echo '> ftuna openrepos mirror --all'
        $ftuna openrepos mirror --all
        pushd "$FTUNA_RATINGS"
            if [ -n "$(git status --porcelain)" ]; then
                git add .
                git commit -m "Update ratings $(date +%Y-%m-%d\ %H:%M:%S)"
                git push
            fi
        popd
#    fi
# done
