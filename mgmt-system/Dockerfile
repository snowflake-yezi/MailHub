FROM node:22-alpine AS web-build
WORKDIR /src/mgmt-system/web
COPY mgmt-system/web/package.json mgmt-system/web/package-lock.json ./
RUN npm ci
COPY mgmt-system/web/ ./
RUN mkdir -p ../template/static/admin-app && npm run build

FROM golang:1.26-alpine AS go-build
WORKDIR /src/mgmt-system
COPY mgmt-system/go.mod mgmt-system/go.sum ./
RUN go mod download
COPY mgmt-system/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/mgmt-server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates openssh-client tzdata
WORKDIR /opt/mgmt-system
COPY --from=go-build /out/mgmt-server ./mgmt-server
COPY mgmt-system/template ./template
COPY --from=web-build /src/mgmt-system/template/static/admin-app ./template/static/admin-app
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/opt/mgmt-system/mgmt-server"]
CMD ["serve"]
