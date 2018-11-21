FROM golang:1.11-alpine as build

COPY . /ipfs-proxy

RUN cd /ipfs-proxy && env GOBIN=/usr/local/bin go install -tags netgo

FROM ipfs/go-ipfs

COPY --from=build /usr/local/bin/ipfs-proxy /usr/local/bin/ipfs-proxy

EXPOSE 8089

ENTRYPOINT []
CMD ["ipfs-proxy"]
