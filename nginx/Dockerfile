ARG REGISTRY_MIRROR=docker.io

FROM ${REGISTRY_MIRROR}/library/node:25-bookworm AS frontend-build
ARG AGENT_COMPOSE_BASE=/
WORKDIR /app
COPY package.json package-lock.json tsconfig.json ./
RUN npm ci
COPY frontend ./frontend
RUN AGENT_COMPOSE_BASE=${AGENT_COMPOSE_BASE} npm run build:ui

FROM ${REGISTRY_MIRROR}/library/nginx:alpine
RUN rm -f /etc/nginx/conf.d/default.conf
COPY nginx/nginx.main /etc/nginx/nginx.conf
COPY nginx/10-normalize-jupyter-proxy-base.envsh /docker-entrypoint.d/10-normalize-jupyter-proxy-base.envsh
RUN chmod +x /docker-entrypoint.d/10-normalize-jupyter-proxy-base.envsh
COPY nginx/nginx.conf /etc/nginx/templates/agent-compose.conf.template
COPY --from=frontend-build /app/dist-ui /srv/agent-compose/frontend
