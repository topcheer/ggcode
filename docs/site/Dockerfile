FROM alpine:3.21 AS site

RUN apk add --no-cache git

WORKDIR /app
COPY . .
RUN sh scripts/site/build.sh

FROM caddy:2.10-alpine

COPY docs/site/Caddyfile /etc/caddy/Caddyfile
COPY --from=site /app/.site-build /srv
