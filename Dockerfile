FROM golang:1.22 as builder
WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download
COPY main.go .

RUN CGO_ENABLED=0 GOOS=linux go build -o /app/bot

FROM alpine:3.19
WORKDIR /app
ARG UID=10001
ARG GID=10001
COPY --from=builder /app/bot /app/bot
RUN addgroup \
    --gid "$GID" \
    bot \
&&  adduser \
    --disabled-password \
    --gecos "" \
    --home "$(pwd)" \
    --ingroup bot \
    --no-create-home \
    --uid "$UID" \
    bot \
&&  chown -R bot:bot /app
USER ${USER_ID}:${GROUP_ID}
COPY app.env.example /app/app.env
ENTRYPOINT ["/app/bot"]