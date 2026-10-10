FROM caddy:2.10-alpine

WORKDIR /srv

COPY Caddyfile /etc/caddy/Caddyfile
COPY . .

RUN asset_version="${RAILWAY_GIT_COMMIT_SHA:-${SOURCE_COMMIT:-$(date +%s)}}" \
  && asset_version="$(printf '%s' "$asset_version" | cut -c1-12)" \
  && find /srv -type f -name '*.html' -exec sed -i "s/__SITE_ASSET_VERSION__/${asset_version}/g" {} +
