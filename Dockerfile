FROM golang:1.26.4-alpine AS build

WORKDIR /build

COPY . .

# TODO: BUMP
ARG VERSION="unknown"
RUN go build -ldflags "-X skulpture/kakak/constants/envs.VERSION=$VERSION" -o kakak .

FROM alpine

COPY --from=build /build/kakak /app/kakak

RUN apk update && apk add --no-cache git-cliff

ENTRYPOINT [ "/app/kakak" ]
