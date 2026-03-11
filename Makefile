.PHONY: proto clean build tidy docker-up docker-down docker-logs

proto:
	protoc -I contract/proto \
		--go_out=contract --go_opt=paths=source_relative \
		--go-grpc_out=contract --go-grpc_opt=paths=source_relative \
		auth/auth.proto user/user.proto notification/notification.proto
	mkdir -p contract/authpb contract/userpb contract/notificationpb
	mv contract/auth/*.pb.go contract/authpb/ 2>/dev/null || true
	mv contract/user/*.pb.go contract/userpb/ 2>/dev/null || true
	mv contract/notification/*.pb.go contract/notificationpb/ 2>/dev/null || true
	rmdir contract/auth contract/user contract/notification 2>/dev/null || true

build:
	cd user-service && go build -o bin/user-service ./cmd
	cd auth-service && go build -o bin/auth-service ./cmd
	cd api-gateway && go build -o bin/api-gateway ./cmd
	cd notification-service && go build -o bin/notification-service ./cmd

tidy:
	cd contract && go mod tidy
	cd user-service && go mod tidy
	cd auth-service && go mod tidy
	cd api-gateway && go mod tidy
	cd notification-service && go mod tidy

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f

clean:
	rm -f contract/authpb/*.go contract/userpb/*.go contract/notificationpb/*.go
	rm -f user-service/bin/* auth-service/bin/* api-gateway/bin/* notification-service/bin/*
