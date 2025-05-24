# Mirror Streaming Platform - Part 6: Infrastructure and Deployment

## Table of Contents
1. [Docker Configuration](#docker-configuration)
2. [AWS Infrastructure with Terraform](#aws-infrastructure-with-terraform)
3. [ECS Deployment](#ecs-deployment)
4. [S3 and CloudFront Setup](#s3-and-cloudfront-setup)
5. [Monitoring Infrastructure](#monitoring-infrastructure)
6. [Security Configuration](#security-configuration)

## Docker Configuration

### deployments/docker/Dockerfile

```dockerfile
# Multi-stage build for optimal image size
FROM golang:1.21-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git gcc musl-dev

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY .. .

# Build the binary
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-w -s -X main.Version=$(git describe --tags --always) \
    -X main.BuildTime=$(date -u '+%Y-%m-%d_%H:%M:%S') \
    -X main.GitCommit=$(git rev-parse HEAD)" \
    -o mirror cmd/mirror/main.go

# Runtime stage
FROM nvidia/cuda:11.8.0-base-ubuntu22.04

# Install runtime dependencies
RUN apt-get update && apt-get install -y \
    ca-certificates \
    tzdata \
    ffmpeg \
    && rm -rf /var/lib/apt/lists/*

# Install NVIDIA Video Codec SDK
RUN apt-get update && apt-get install -y \
    libnvidia-encode-515 \
    libnvidia-decode-515 \
    && rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN useradd -m -u 1000 mirror

# Copy binary from builder
COPY --from=builder /build/mirror /usr/local/bin/mirror

# Copy default config
COPY --from=builder /build/configs/mirror.yaml /etc/mirror/mirror.yaml

# Create necessary directories
RUN mkdir -p /var/lib/mirror/cache /var/log/mirror && \
    chown -R mirror:mirror /var/lib/mirror /var/log/mirror /etc/mirror

# Switch to non-root user
USER mirror

# Expose ports
EXPOSE 443 6000/udp 5004/udp 8080

# Set environment variables
ENV NVIDIA_VISIBLE_DEVICES=all \
    NVIDIA_DRIVER_CAPABILITIES=video,compute,utility \
    MIRROR_CONFIG=/etc/mirror/mirror.yaml

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/mirror", "health"] || exit 1

# Entry point
ENTRYPOINT ["/usr/local/bin/mirror"]
```

### deployments/docker/docker-compose.yml
```yaml
version: '3.8'

services:
  mirror:
    build:
      context: ../..
      dockerfile: deployments/docker/Dockerfile
    image: mirror:latest
    container_name: mirror-streaming
    restart: unless-stopped
    
    # GPU support
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
    
    ports:
      - "443:443"              # HTTPS/HTTP3
      - "6000:6000/udp"        # SRT
      - "5004:5004/udp"        # RTP
      - "8080:8080"            # Admin API
    
    volumes:
      - mirror-cache:/var/lib/mirror/cache
      - mirror-logs:/var/log/mirror
      - ./configs/mirror.yaml:/etc/mirror/mirror.yaml:ro
    
    environment:
      - MIRROR_STORAGE_S3_ACCESS_KEY_ID=${AWS_ACCESS_KEY_ID}
      - MIRROR_STORAGE_S3_SECRET_ACCESS_KEY=${AWS_SECRET_ACCESS_KEY}
      - MIRROR_STORAGE_S3_REGION=${AWS_REGION:-us-east-1}
      - MIRROR_STORAGE_S3_BUCKET=${S3_BUCKET:-mirror-streams}
      - MIRROR_REDIS_ADDR=redis:6379
    
    depends_on:
      - redis
    
    networks:
      - mirror-net

  redis:
    image: redis:7-alpine
    container_name: mirror-redis
    restart: unless-stopped
    command: redis-server --appendonly yes --maxmemory 2gb --maxmemory-policy allkeys-lru
    volumes:
      - redis-data:/data
    networks:
      - mirror-net

  prometheus:
    image: prom/prometheus:latest
    container_name: mirror-prometheus
    restart: unless-stopped
    volumes:
      - ./monitoring/prometheus.yml:/etc/prometheus/prometheus.yml
      - prometheus-data:/prometheus
    command:
      - '--config.file=/etc/prometheus/prometheus.yml'
      - '--storage.tsdb.path=/prometheus'
      - '--web.console.libraries=/usr/share/prometheus/console_libraries'
      - '--web.console.templates=/usr/share/prometheus/consoles'
    ports:
      - "9090:9090"
    networks:
      - mirror-net

  grafana:
    image: grafana/grafana:latest
    container_name: mirror-grafana
    restart: unless-stopped
    volumes:
      - grafana-data:/var/lib/grafana
      - ./monitoring/grafana/provisioning:/etc/grafana/provisioning
      - ./monitoring/grafana/dashboards:/var/lib/grafana/dashboards
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
      - GF_USERS_ALLOW_SIGN_UP=false
    ports:
      - "3000:3000"
    networks:
      - mirror-net

volumes:
  mirror-cache:
  mirror-logs:
  redis-data:
  prometheus-data:
  grafana-data:

networks:
  mirror-net:
    driver: bridge
```

## AWS Infrastructure with Terraform

### deployments/terraform/main.tf
```hcl
terraform {
  required_version = ">= 1.0"
  
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
  
  backend "s3" {
    bucket = "mirror-terraform-state"
    key    = "infrastructure/terraform.tfstate"
    region = "us-east-1"
  }
}

provider "aws" {
  region = var.aws_region
  
  default_tags {
    tags = {
      Project     = "Mirror"
      Environment = var.environment
      ManagedBy   = "Terraform"
    }
  }
}

# Data sources
data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_caller_identity" "current" {}
```

### deployments/terraform/variables.tf
```hcl
variable "aws_region" {
  description = "AWS region for deployment"
  type        = string
  default     = "us-east-1"
}

variable "environment" {
  description = "Environment name (dev, staging, prod)"
  type        = string
  default     = "prod"
}

variable "project_name" {
  description = "Project name"
  type        = string
  default     = "mirror"
}

variable "vpc_cidr" {
  description = "CIDR block for VPC"
  type        = string
  default     = "10.0.0.0/16"
}

variable "gpu_instance_type" {
  description = "EC2 instance type for GPU workloads"
  type        = string
  default     = "g5.2xlarge"
}

variable "min_capacity" {
  description = "Minimum number of instances"
  type        = number
  default     = 1
}

variable "max_capacity" {
  description = "Maximum number of instances"
  type        = number
  default     = 4
}

variable "domain_name" {
  description = "Domain name for CloudFront distribution"
  type        = string
  default     = ""
}
```

### deployments/terraform/network.tf
```hcl
# VPC
resource "aws_vpc" "main" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "${var.project_name}-${var.environment}-vpc"
  }
}

# Internet Gateway
resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id

  tags = {
    Name = "${var.project_name}-${var.environment}-igw"
  }
}

# Public Subnets
resource "aws_subnet" "public" {
  count             = 2
  vpc_id            = aws_vpc.main.id
  cidr_block        = cidrsubnet(var.vpc_cidr, 8, count.index)
  availability_zone = data.aws_availability_zones.available.names[count.index]
  
  map_public_ip_on_launch = true

  tags = {
    Name = "${var.project_name}-${var.environment}-public-${count.index + 1}"
    Type = "public"
  }
}

# Private Subnets
resource "aws_subnet" "private" {
  count             = 2
  vpc_id            = aws_vpc.main.id
  cidr_block        = cidrsubnet(var.vpc_cidr, 8, count.index + 10)
  availability_zone = data.aws_availability_zones.available.names[count.index]

  tags = {
    Name = "${var.project_name}-${var.environment}-private-${count.index + 1}"
    Type = "private"
  }
}

# NAT Gateway
resource "aws_eip" "nat" {
  count  = 2
  domain = "vpc"

  tags = {
    Name = "${var.project_name}-${var.environment}-nat-${count.index + 1}"
  }
}

resource "aws_nat_gateway" "main" {
  count         = 2
  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id

  tags = {
    Name = "${var.project_name}-${var.environment}-nat-${count.index + 1}"
  }
}

# Route Tables
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.main.id
  }

  tags = {
    Name = "${var.project_name}-${var.environment}-public-rt"
  }
}

resource "aws_route_table" "private" {
  count  = 2
  vpc_id = aws_vpc.main.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.main[count.index].id
  }

  tags = {
    Name = "${var.project_name}-${var.environment}-private-rt-${count.index + 1}"
  }
}

# Route Table Associations
resource "aws_route_table_association" "public" {
  count          = 2
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table_association" "private" {
  count          = 2
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# Security Groups
resource "aws_security_group" "alb" {
  name        = "${var.project_name}-${var.environment}-alb-sg"
  description = "Security group for Application Load Balancer"
  vpc_id      = aws_vpc.main.id

  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "HTTPS"
  }

  ingress {
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "HTTP"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.project_name}-${var.environment}-alb-sg"
  }
}

resource "aws_security_group" "ecs" {
  name        = "${var.project_name}-${var.environment}-ecs-sg"
  description = "Security group for ECS tasks"
  vpc_id      = aws_vpc.main.id

  ingress {
    from_port       = 443
    to_port         = 443
    protocol        = "tcp"
    security_groups = [aws_security_group.alb.id]
    description     = "HTTPS from ALB"
  }

  ingress {
    from_port   = 8080
    to_port     = 8080
    protocol    = "tcp"
    security_groups = [aws_security_group.alb.id]
    description = "Admin API from ALB"
  }

  ingress {
    from_port   = 6000
    to_port     = 6000
    protocol    = "udp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "SRT streaming"
  }

  ingress {
    from_port   = 5004
    to_port     = 5004
    protocol    = "udp"
    cidr_blocks = ["0.0.0.0/0"]
    description = "RTP streaming"
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "${var.project_name}-${var.environment}-ecs-sg"
  }
}

# VPC Endpoints for S3
resource "aws_vpc_endpoint" "s3" {
  vpc_id       = aws_vpc.main.id
  service_name = "com.amazonaws.${var.aws_region}.s3"
  
  tags = {
    Name = "${var.project_name}-${var.environment}-s3-endpoint"
  }
}

resource "aws_vpc_endpoint_route_table_association" "s3_private" {
  count           = 2
  route_table_id  = aws_route_table.private[count.index].id
  vpc_endpoint_id = aws_vpc_endpoint.s3.id
}
```

## ECS Deployment

### deployments/terraform/ecs.tf
```hcl
# ECS Cluster
resource "aws_ecs_cluster" "main" {
  name = "${var.project_name}-${var.environment}"

  setting {
    name  = "containerInsights"
    value = "enabled"
  }

  tags = {
    Name = "${var.project_name}-${var.environment}-cluster"
  }
}

# ECS Capacity Provider for GPU instances
resource "aws_ecs_capacity_provider" "gpu" {
  name = "${var.project_name}-${var.environment}-gpu"

  auto_scaling_group_provider {
    auto_scaling_group_arn = aws_autoscaling_group.gpu.arn
    
    managed_scaling {
      maximum_scaling_step_size = 2
      minimum_scaling_step_size = 1
      status                    = "ENABLED"
      target_capacity           = 80
    }
  }
}

resource "aws_ecs_cluster_capacity_providers" "main" {
  cluster_name = aws_ecs_cluster.main.name

  capacity_providers = [
    aws_ecs_capacity_provider.gpu.name
  ]

  default_capacity_provider_strategy {
    base              = 1
    weight            = 100
    capacity_provider = aws_ecs_capacity_provider.gpu.name
  }
}

# Launch Template for GPU instances
resource "aws_launch_template" "gpu" {
  name_prefix   = "${var.project_name}-${var.environment}-gpu-"
  image_id      = data.aws_ami.ecs_gpu.id
  instance_type = var.gpu_instance_type

  iam_instance_profile {
    name = aws_iam_instance_profile.ecs_instance.name
  }

  vpc_security_group_ids = [aws_security_group.ecs.id]

  user_data = base64encode(templatefile("${path.module}/user_data.sh", {
    cluster_name = aws_ecs_cluster.main.name
  }))

  block_device_mappings {
    device_name = "/dev/xvda"

    ebs {
      volume_size           = 100
      volume_type           = "gp3"
      delete_on_termination = true
      encrypted             = true
    }
  }

  tag_specifications {
    resource_type = "instance"
    tags = {
      Name = "${var.project_name}-${var.environment}-gpu-instance"
    }
  }
}

# Auto Scaling Group
resource "aws_autoscaling_group" "gpu" {
  name               = "${var.project_name}-${var.environment}-gpu-asg"
  vpc_zone_identifier = aws_subnet.private[*].id
  min_size           = var.min_capacity
  max_size           = var.max_capacity
  desired_capacity   = var.min_capacity

  launch_template {
    id      = aws_launch_template.gpu.id
    version = "$Latest"
  }

  tag {
    key                 = "Name"
    value               = "${var.project_name}-${var.environment}-gpu-instance"
    propagate_at_launch = true
  }

  tag {
    key                 = "AmazonECSManaged"
    value               = "true"
    propagate_at_launch = true
  }
}

# Task Definition
resource "aws_ecs_task_definition" "mirror" {
  family                   = "${var.project_name}-${var.environment}"
  network_mode             = "awsvpc"
  requires_compatibilities = ["EC2"]
  cpu                      = "8192"
  memory                   = "30720"
  task_role_arn            = aws_iam_role.ecs_task.arn
  execution_role_arn       = aws_iam_role.ecs_execution.arn

  container_definitions = jsonencode([
    {
      name  = "mirror"
      image = "${aws_ecr_repository.mirror.repository_url}:latest"
      
      essential = true
      
      portMappings = [
        {
          containerPort = 443
          protocol      = "tcp"
        },
        {
          containerPort = 8080
          protocol      = "tcp"
        },
        {
          containerPort = 6000
          protocol      = "udp"
        },
        {
          containerPort = 5004
          protocol      = "udp"
        }
      ]
      
      environment = [
        {
          name  = "MIRROR_STORAGE_S3_REGION"
          value = var.aws_region
        },
        {
          name  = "MIRROR_STORAGE_S3_BUCKET"
          value = aws_s3_bucket.streams.id
        },
        {
          name  = "MIRROR_REDIS_ADDR"
          value = "${aws_elasticache_cluster.redis.cache_nodes[0].address}:6379"
        }
      ]
      
      secrets = [
        {
          name      = "MIRROR_STORAGE_S3_ACCESS_KEY_ID"
          valueFrom = aws_secretsmanager_secret.s3_credentials.arn
        },
        {
          name      = "MIRROR_STORAGE_S3_SECRET_ACCESS_KEY"
          valueFrom = aws_secretsmanager_secret.s3_credentials.arn
        }
      ]
      
      resourceRequirements = [
        {
          type  = "GPU"
          value = "1"
        }
      ]
      
      logConfiguration = {
        logDriver = "awslogs"
        options = {
          "awslogs-group"         = aws_cloudwatch_log_group.mirror.name
          "awslogs-region"        = var.aws_region
          "awslogs-stream-prefix" = "ecs"
        }
      }
      
      healthCheck = {
        command     = ["CMD-SHELL", "/usr/local/bin/mirror health || exit 1"]
        interval    = 30
        timeout     = 5
        retries     = 3
        startPeriod = 60
      }
    }
  ])
}

# ECS Service
resource "aws_ecs_service" "mirror" {
  name            = "${var.project_name}-${var.environment}"
  cluster         = aws_ecs_cluster.main.id
  task_definition = aws_ecs_task_definition.mirror.arn
  desired_count   = var.min_capacity

  capacity_provider_strategy {
    capacity_provider = aws_ecs_capacity_provider.gpu.name
    weight            = 100
    base              = 1
  }

  network_configuration {
    subnets         = aws_subnet.private[*].id
    security_groups = [aws_security_group.ecs.id]
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.https.arn
    container_name   = "mirror"
    container_port   = 443
  }

  load_balancer {
    target_group_arn = aws_lb_target_group.admin.arn
    container_name   = "mirror"
    container_port   = 8080
  }

  depends_on = [
    aws_lb_listener.https,
    aws_lb_listener.admin
  ]
}
```

### deployments/terraform/user_data.sh
```bash
#!/bin/bash
echo ECS_CLUSTER=${cluster_name} >> /etc/ecs/ecs.config
echo ECS_ENABLE_GPU_SUPPORT=true >> /etc/ecs/ecs.config
echo ECS_NVIDIA_RUNTIME=nvidia >> /etc/ecs/ecs.config

# Install NVIDIA drivers if not present
if ! nvidia-smi &> /dev/null; then
  yum install -y nvidia-driver-latest-dkms
  yum install -y nvidia-container-toolkit
  systemctl restart docker
fi

# Install CloudWatch agent
wget https://s3.amazonaws.com/amazoncloudwatch-agent/amazon_linux/amd64/latest/amazon-cloudwatch-agent.rpm
rpm -U ./amazon-cloudwatch-agent.rpm
```

## S3 and CloudFront Setup

### deployments/terraform/storage.tf
```hcl
# S3 Bucket for stream segments
resource "aws_s3_bucket" "streams" {
  bucket = "${var.project_name}-${var.environment}-streams"

  tags = {
    Name = "${var.project_name}-${var.environment}-streams"
  }
}

resource "aws_s3_bucket_versioning" "streams" {
  bucket = aws_s3_bucket.streams.id
  
  versioning_configuration {
    status = "Disabled"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "streams" {
  bucket = aws_s3_bucket.streams.id

  rule {
    id     = "delete-old-segments"
    status = "Enabled"

    filter {
      prefix = "streams/"
    }

    expiration {
      days = 1
    }
  }

  rule {
    id     = "delete-old-dvr"
    status = "Enabled"

    filter {
      prefix = "dvr/"
    }

    expiration {
      days = 7
    }
  }
}

resource "aws_s3_bucket_cors_configuration" "streams" {
  bucket = aws_s3_bucket.streams.id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD"]
    allowed_origins = ["*"]
    expose_headers  = ["ETag"]
    max_age_seconds = 3000
  }
}

resource "aws_s3_bucket_public_access_block" "streams" {
  bucket = aws_s3_bucket.streams.id

  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# CloudFront Distribution
resource "aws_cloudfront_distribution" "streams" {
  enabled             = true
  is_ipv6_enabled     = true
  comment             = "${var.project_name} ${var.environment} streaming CDN"
  default_root_object = ""
  
  aliases = var.domain_name != "" ? [var.domain_name] : []

  origin {
    domain_name = aws_s3_bucket.streams.bucket_regional_domain_name
    origin_id   = "S3-${aws_s3_bucket.streams.id}"

    s3_origin_config {
      origin_access_identity = aws_cloudfront_origin_access_identity.streams.cloudfront_access_identity_path
    }
  }

  origin {
    domain_name = aws_lb.main.dns_name
    origin_id   = "ALB-${aws_lb.main.id}"

    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }

  # Default cache behavior for HLS segments
  default_cache_behavior {
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "S3-${aws_s3_bucket.streams.id}"

    forwarded_values {
      query_string = true
      headers      = ["Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"]

      cookies {
        forward = "none"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 1
    max_ttl                = 3

    compress = true
  }

  # Cache behavior for playlists
  ordered_cache_behavior {
    path_pattern     = "*.m3u8"
    allowed_methods  = ["GET", "HEAD", "OPTIONS"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "ALB-${aws_lb.main.id}"

    forwarded_values {
      query_string = true
      headers      = ["*"]

      cookies {
        forward = "all"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 0
    max_ttl                = 0
  }

  # Cache behavior for segments
  ordered_cache_behavior {
    path_pattern     = "*.m4s"
    allowed_methods  = ["GET", "HEAD"]
    cached_methods   = ["GET", "HEAD"]
    target_origin_id = "S3-${aws_s3_bucket.streams.id}"

    forwarded_values {
      query_string = true

      cookies {
        forward = "none"
      }
    }

    viewer_protocol_policy = "redirect-to-https"
    min_ttl                = 0
    default_ttl            = 3600
    max_ttl                = 86400

    compress = true
  }

  price_class = "PriceClass_100"

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = var.domain_name == "" ? true : false
    acm_certificate_arn            = var.domain_name != "" ? aws_acm_certificate.cdn[0].arn : null
    ssl_support_method             = "sni-only"
    minimum_protocol_version       = "TLSv1.2_2021"
  }

  tags = {
    Name = "${var.project_name}-${var.environment}-cdn"
  }
}

resource "aws_cloudfront_origin_access_identity" "streams" {
  comment = "${var.project_name} ${var.environment} OAI"
}

# S3 Bucket Policy for CloudFront
resource "aws_s3_bucket_policy" "streams" {
  bucket = aws_s3_bucket.streams.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Sid    = "AllowCloudFrontAccess"
        Effect = "Allow"
        Principal = {
          AWS = aws_cloudfront_origin_access_identity.streams.iam_arn
        }
        Action   = "s3:GetObject"
        Resource = "${aws_s3_bucket.streams.arn}/*"
      }
    ]
  })
}
```

## Monitoring Infrastructure

### deployments/terraform/monitoring.tf
```hcl
# CloudWatch Log Group
resource "aws_cloudwatch_log_group" "mirror" {
  name              = "/ecs/${var.project_name}-${var.environment}"
  retention_in_days = 7

  tags = {
    Name = "${var.project_name}-${var.environment}-logs"
  }
}

# CloudWatch Dashboard
resource "aws_cloudwatch_dashboard" "mirror" {
  dashboard_name = "${var.project_name}-${var.environment}"

  dashboard_body = jsonencode({
    widgets = [
      {
        type   = "metric"
        x      = 0
        y      = 0
        width  = 12
        height = 6

        properties = {
          metrics = [
            ["AWS/ECS", "CPUUtilization", "ClusterName", aws_ecs_cluster.main.name],
            [".", "MemoryUtilization", ".", "."],
            ["AWS/EC2", "GPUUtilization", "AutoScalingGroupName", aws_autoscaling_group.gpu.name]
          ]
          period = 300
          stat   = "Average"
          region = var.aws_region
          title  = "Resource Utilization"
        }
      },
      {
        type   = "metric"
        x      = 12
        y      = 0
        width  = 12
        height = 6

        properties = {
          metrics = [
            ["AWS/ApplicationELB", "TargetResponseTime", "LoadBalancer", aws_lb.main.arn_suffix],
            [".", "RequestCount", ".", "."],
            [".", "TargetConnectionErrorCount", ".", "."]
          ]
          period = 300
          stat   = "Sum"
          region = var.aws_region
          title  = "Load Balancer Metrics"
        }
      },
      {
        type   = "metric"
        x      = 0
        y      = 6
        width  = 24
        height = 6

        properties = {
          metrics = [
            ["AWS/CloudFront", "BytesDownloaded", "DistributionId", aws_cloudfront_distribution.streams.id],
            [".", "Requests", ".", "."],
            [".", "CacheHitRate", ".", "."]
          ]
          period = 300
          stat   = "Sum"
          region = "us-east-1"
          title  = "CloudFront Metrics"
        }
      }
    ]
  })
}

# CloudWatch Alarms
resource "aws_cloudwatch_metric_alarm" "cpu_high" {
  alarm_name          = "${var.project_name}-${var.environment}-cpu-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "CPUUtilization"
  namespace           = "AWS/ECS"
  period              = "300"
  statistic           = "Average"
  threshold           = "80"
  alarm_description   = "This metric monitors CPU utilization"
  alarm_actions       = [aws_sns_topic.alerts.arn]

  dimensions = {
    ClusterName = aws_ecs_cluster.main.name
  }
}

resource "aws_cloudwatch_metric_alarm" "gpu_high" {
  alarm_name          = "${var.project_name}-${var.environment}-gpu-high"
  comparison_operator = "GreaterThanThreshold"
  evaluation_periods  = "2"
  metric_name         = "GPUUtilization"
  namespace           = "AWS/EC2"
  period              = "300"
  statistic           = "Average"
  threshold           = "90"
  alarm_description   = "This metric monitors GPU utilization"
  alarm_actions       = [aws_sns_topic.alerts.arn]

  dimensions = {
    AutoScalingGroupName = aws_autoscaling_group.gpu.name
  }
}

# SNS Topic for Alerts
resource "aws_sns_topic" "alerts" {
  name = "${var.project_name}-${var.environment}-alerts"

  tags = {
    Name = "${var.project_name}-${var.environment}-alerts"
  }
}

resource "aws_sns_topic_subscription" "alerts_email" {
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = var.alert_email
}
```

### deployments/monitoring/prometheus.yml
```yaml
global:
  scrape_interval: 15s
  evaluation_interval: 15s

scrape_configs:
  - job_name: 'mirror'
    static_configs:
      - targets: ['mirror:8080']
    metrics_path: '/metrics'

  - job_name: 'node-exporter'
    static_configs:
      - targets: ['node-exporter:9100']

  - job_name: 'nvidia-gpu'
    static_configs:
      - targets: ['nvidia-exporter:9445']
```

### deployments/monitoring/grafana/dashboards/mirror.json
```json
{
  "dashboard": {
    "id": null,
    "title": "Mirror Streaming Platform",
    "tags": ["streaming", "mirror"],
    "timezone": "browser",
    "panels": [
      {
        "gridPos": {
          "h": 8,
          "w": 12,
          "x": 0,
          "y": 0
        },
        "id": 1,
        "targets": [
          {
            "expr": "mirror_active_streams_total",
            "refId": "A"
          }
        ],
        "title": "Active Streams",
        "type": "graph"
      },
      {
        "gridPos": {
          "h": 8,
          "w": 12,
          "x": 12,
          "y": 0
        },
        "id": 2,
        "targets": [
          {
            "expr": "rate(mirror_stream_bytes_total[5m])",
            "refId": "A"
          }
        ],
        "title": "Stream Bitrate",
        "type": "graph"
      },
      {
        "gridPos": {
          "h": 8,
          "w": 12,
          "x": 0,
          "y": 8
        },
        "id": 3,
        "targets": [
          {
            "expr": "rate(mirror_segments_generated_total[5m])",
            "refId": "A"
          }
        ],
        "title": "Segments Generated",
        "type": "graph"
      },
      {
        "gridPos": {
          "h": 8,
          "w": 12,
          "x": 12,
          "y": 8
        },
        "id": 4,
        "targets": [
          {
            "expr": "nvidia_gpu_utilization",
            "refId": "A"
          }
        ],
        "title": "GPU Utilization",
        "type": "graph"
      }
    ],
    "schemaVersion": 16,
    "version": 0
  }
}
```

## Security Configuration

### deployments/terraform/security.tf
```hcl
# KMS Key for encryption
resource "aws_kms_key" "mirror" {
  description             = "${var.project_name} ${var.environment} encryption key"
  deletion_window_in_days = 10
  enable_key_rotation     = true

  tags = {
    Name = "${var.project_name}-${var.environment}-kms"
  }
}

resource "aws_kms_alias" "mirror" {
  name          = "alias/${var.project_name}-${var.environment}"
  target_key_id = aws_kms_key.mirror.key_id
}

# Secrets Manager for credentials
resource "aws_secretsmanager_secret" "s3_credentials" {
  name = "${var.project_name}-${var.environment}-s3-credentials"

  tags = {
    Name = "${var.project_name}-${var.environment}-s3-credentials"
  }
}

resource "aws_secretsmanager_secret_version" "s3_credentials" {
  secret_id = aws_secretsmanager_secret.s3_credentials.id
  
  secret_string = jsonencode({
    access_key_id     = aws_iam_access_key.s3_user.id
    secret_access_key = aws_iam_access_key.s3_user.secret
  })
}

# WAF for CloudFront
resource "aws_wafv2_web_acl" "mirror" {
  name  = "${var.project_name}-${var.environment}-waf"
  scope = "CLOUDFRONT"

  default_action {
    allow {}
  }

  # Rate limiting rule
  rule {
    name     = "RateLimitRule"
    priority = 1

    action {
      block {}
    }

    statement {
      rate_based_statement {
        limit              = 2000
        aggregate_key_type = "IP"
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${var.project_name}-${var.environment}-rate-limit"
      sampled_requests_enabled   = true
    }
  }

  # Geo blocking rule (example)
  rule {
    name     = "GeoBlockingRule"
    priority = 2

    action {
      block {}
    }

    statement {
      geo_match_statement {
        country_codes = ["CN", "RU", "KP"]
      }
    }

    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "${var.project_name}-${var.environment}-geo-block"
      sampled_requests_enabled   = true
    }
  }

  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "${var.project_name}-${var.environment}-waf"
    sampled_requests_enabled   = true
  }

  tags = {
    Name = "${var.project_name}-${var.environment}-waf"
  }
}

resource "aws_wafv2_web_acl_association" "mirror" {
  resource_arn = aws_cloudfront_distribution.streams.arn
  web_acl_arn  = aws_wafv2_web_acl.mirror.arn
}

# Security Hub
resource "aws_securityhub_account" "main" {
  count = var.enable_security_hub ? 1 : 0
}

resource "aws_securityhub_standards_subscription" "cis" {
  count         = var.enable_security_hub ? 1 : 0
  standards_arn = "arn:aws:securityhub:${var.aws_region}::standards/cis-aws-foundations-benchmark/v/1.2.0"
  
  depends_on = [aws_securityhub_account.main]
}
```

### deployments/terraform/outputs.tf
```hcl
output "alb_dns_name" {
  description = "DNS name of the load balancer"
  value       = aws_lb.main.dns_name
}

output "cloudfront_domain_name" {
  description = "CloudFront distribution domain name"
  value       = aws_cloudfront_distribution.streams.domain_name
}

output "s3_bucket_name" {
  description = "Name of the S3 bucket for streams"
  value       = aws_s3_bucket.streams.id
}

output "ecr_repository_url" {
  description = "URL of the ECR repository"
  value       = aws_ecr_repository.mirror.repository_url
}

output "ecs_cluster_name" {
  description = "Name of the ECS cluster"
  value       = aws_ecs_cluster.main.name
}

output "redis_endpoint" {
  description = "Redis cluster endpoint"
  value       = aws_elasticache_cluster.redis.cache_nodes[0].address
}
```

## Next Steps

Continue to:
- [Part 7: Development Setup and CI/CD](plan-7.md) - Local development environment and GitHub Actions