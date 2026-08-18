FROM --platform=$TARGETPLATFORM golang:1.22
WORKDIR /app
COPY go.mod go.sum ./
RUN GOTOOLCHAIN=local go mod download
COPY . .
RUN GOTOOLCHAIN=local go build ./...
CMD ["bash"]
