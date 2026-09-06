ARG GO_VERSION
FROM node:26-alpine@sha256:2d984a15c9b54fd0aeb608b8e0d0d83529eb34d2966db27a1fb4f1edc3d298a3 AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build
FROM golang:${GO_VERSION}-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468 AS server
ARG GO_VERSION
RUN test "$(go env GOVERSION)" = "go${GO_VERSION}"
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY sdk ./sdk
COPY app ./app
COPY worker ./worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /providah ./cmd/providah && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /provider ./cmd/provider && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /launcher ./cmd/launcher && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /egress-proxy ./cmd/egress-proxy
RUN mkdir -p /egress
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS runtime
ARG EDITION=community
ARG SOURCE=https://github.com/alphabravo-oss/providah-community
ARG REVISION=development
ARG VERSION=development
LABEL org.opencontainers.image.source=$SOURCE org.opencontainers.image.revision=$REVISION org.opencontainers.image.version=$VERSION io.providah.edition=$EDITION
FROM runtime AS provider
COPY --from=server /provider /provider
ENTRYPOINT ["/provider"]
FROM runtime AS egress
COPY --from=server /egress-proxy /egress-proxy
COPY --from=server --chown=65532:65532 /egress/ /run/egress/
USER 65532:65532
HEALTHCHECK --interval=1s --timeout=1s --retries=3 CMD ["/egress-proxy", "health"]
ENTRYPOINT ["/egress-proxy"]
FROM docker:cli@sha256:eccaacfeed644c7de222ff047483568cb988dde95476fbaaf10ea2d04921bb66 AS launcher
ARG EDITION=community
ARG SOURCE=https://github.com/alphabravo-oss/providah-community
ARG REVISION=development
ARG VERSION=development
LABEL org.opencontainers.image.source=$SOURCE org.opencontainers.image.revision=$REVISION org.opencontainers.image.version=$VERSION io.providah.edition=$EDITION
COPY --from=server /launcher /launcher
HEALTHCHECK --interval=2s --timeout=3s --start-period=360s --retries=5 CMD ["/launcher", "health"]
ENTRYPOINT ["/launcher"]
FROM runtime AS app
COPY --from=server /providah /providah
COPY --from=web /src/web/dist /web
EXPOSE 8080
ENTRYPOINT ["/providah"]
