ARG TAG="latest"
FROM skulpture/kakak-bot:${TAG}

COPY . .

RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
