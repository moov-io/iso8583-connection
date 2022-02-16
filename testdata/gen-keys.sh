#!/bin/bash

# server
openssl req -newkey rsa:2048 -nodes -x509 -days 10000 -out ca.crt -keyout ca.key -subj /C=US
openssl genrsa -out server.key 2048
openssl req -new -key server.key -days 10000 -out server.csr -subj /C=US/CN=127.0.0.1
openssl x509  -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out server.crt -days 1000 -sha256 -extfile domain.ext


# client
openssl genrsa -out client.key 2048
openssl req -new -key client.key -days 10000 -out client.csr -subj /C=US/CN=127.0.0.1
openssl x509  -req -in client.csr -CA ca.crt -CAkey ca.key -out client.crt -days 10000 -sha256 -CAcreateserial

