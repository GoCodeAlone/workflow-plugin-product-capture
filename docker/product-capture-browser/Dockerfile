FROM node:24-bookworm-slim

ENV NODE_PATH=/usr/local/lib/node_modules \
    HOME=/tmp \
    XDG_CACHE_HOME=/tmp/.cache \
    XDG_CONFIG_HOME=/tmp/.config \
    PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1

RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl gnupg; \
    install -d -m 0755 /etc/apt/keyrings; \
    curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /etc/apt/keyrings/google-linux.gpg; \
    chmod 0644 /etc/apt/keyrings/google-linux.gpg; \
    echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/google-linux.gpg] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list; \
    apt-get update; \
    apt-get install -y --no-install-recommends google-chrome-stable xauth xvfb; \
    npm install -g playwright@1.57.0; \
    npm cache clean --force; \
    apt-get purge -y --auto-remove curl gnupg; \
    rm -rf /var/lib/apt/lists/*

COPY docker/product-capture-browser/product-capture-provider /usr/local/bin/product-capture-provider

USER node
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/product-capture-provider"]
