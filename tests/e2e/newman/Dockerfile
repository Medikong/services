FROM postman/newman:6-alpine

WORKDIR /etc/newman
COPY newman/docker.postman_environment.json newman/docker.postman_environment.json
COPY scenarios scenarios

ENTRYPOINT ["newman"]
