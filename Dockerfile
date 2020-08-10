FROM golang:1.12-alpine as backend

WORKDIR /port-manager

COPY ./go.* ./
COPY ./cmd ./cmd
COPY ./internal ./internal
COPY ./Makefile ./
COPY ./vendor ./vendor


RUN apk add --update --no-cache bash curl git make

RUN make build
RUN cp ./bin/port-manager /bin

FROM alpine:3.7
COPY --from=backend /bin /bin

ENTRYPOINT ["/bin/port-manager"]
