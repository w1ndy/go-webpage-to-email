FROM golang:latest AS build
ADD . /src
RUN cd /src \
 && go build ./...

FROM alpine:latest
RUN apk add --no-cache libc6-compat
WORKDIR /app
COPY --from=build /src/daemon /app/
COPY examples /app/
USER nobody
ENTRYPOINT ["/app/daemon"]
