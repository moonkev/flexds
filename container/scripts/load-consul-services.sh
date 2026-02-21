#!/bin/bash

# Default to 10 services if no argument provided
NUM_SERVICES=${1:-10}

echo "Registering ${NUM_SERVICES} services with random names and hosts..."

# Array of random prefixes for service names
prefixes=("api" "web" "auth" "data" "cache" "queue" "worker" "search" "analytics" "payment" "user" "order" "inventory" "notification" "email" "sms" "chat" "video" "image" "file" "storage" "backup" "monitor" "log" "metric" "trace" "gateway" "proxy" "lb" "cdn" "dns" "mail" "ftp" "ssh" "vpn" "db" "redis" "mongo" "postgres" "mysql" "elastic" "kafka" "rabbit" "nats" "grpc" "rest" "graphql" "soap" "rpc")

# Array of random suffixes
suffixes=("service" "srv" "api" "handler" "processor" "manager" "controller" "engine" "daemon" "worker" "agent" "server" "app" "svc")

# Function to generate a random service name
generate_service_name() {
  local prefix=${prefixes[$RANDOM % ${#prefixes[@]}]}
  local suffix=${suffixes[$RANDOM % ${#suffixes[@]}]}
  local num=$((RANDOM % 10000))
  echo "${prefix}-${suffix}-${num}"
}

# Function to generate a random hostname
generate_hostname() {
  local region=("us-east" "us-west" "eu-central" "eu-west" "ap-south" "ap-north")
  local zone=$((RANDOM % 3 + 1))
  local host=$((RANDOM % 100))
  echo "${region[$RANDOM % ${#region[@]}]}-${zone}-host-${host}.example.com"
}

# Register services
for i in $(seq 1 $NUM_SERVICES); do
  service_name=$(generate_service_name)
  hostname=$(generate_hostname)
  port=$((8000 + RANDOM % 2000))

  # Print progress every 100 services
  if [ $((i % 100)) -eq 0 ]; then
    echo "Registered $i services..."
  fi

  # Register the service
  curl -s -X PUT http://localhost:18500/v1/agent/service/register \
    -d "{
      \"ID\": \"${service_name}-${i}\",
      \"Name\": \"${service_name}\",
      \"Port\": ${port},
      \"Address\": \"${hostname}\",
      \"Meta\": {
        \"route_1_match_type\": \"path\",
        \"route_1_path_prefix\": \"/${service_name}\",
        \"route_1_prefix_rewrite\": \"/\",
        \"route_2_match_type\": \"header\",
        \"route_2_header_name\": \"x-service\",
        \"route_2_header_value\": \"${service_name}\",
        \"dns_refresh_rate\": \"60m\"
      }
    }" > /dev/null
done

echo "Successfully registered ${NUM_SERVICES} services!"
