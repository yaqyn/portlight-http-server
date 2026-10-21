FROM golang:1.27.3-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS build
WORKDIR /src
COPY go.mod ./
COPY main.go http.go ./
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /portlight .

FROM scratch
ARG VERSION=dev
LABEL org.opencontainers.image.title="Portlight — HTTP Server" \
      org.opencontainers.image.description="A focused Go HTTP service with clear operational signals" \
      org.opencontainers.image.source="https://github.com/yaqyn/portlight-http-server" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /portlight /portlight
USER 65532:65532
ENV PORT=8991
EXPOSE 8991
STOPSIGNAL SIGTERM
ENTRYPOINT ["/portlight"]
