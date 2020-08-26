FROM golang:1.15-alpine as build

RUN apk add --no-cache git mercurial

COPY . /ipfs-proxy

RUN cd /ipfs-proxy && go build -tags netgo

FROM scratch

COPY --from=build /ipfs-proxy/ipfs-proxy /ipfs-proxy

EXPOSE 8089

ENTRYPOINT []
CMD ["/ipfs-proxy"]
