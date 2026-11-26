FROM golang:1.22-alpine AS build

RUN apk add --no-cache ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gen-ai-prv . \
    && mkdir -m 1777 /tmp-runtime

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build --chown=0:0 --chmod=1777 /tmp-runtime /tmp
COPY --from=build /out/gen-ai-prv /gen-ai-prv

USER 10001:10001
ENTRYPOINT ["/gen-ai-prv"]
CMD ["serve", "--host", "0.0.0.0"]
