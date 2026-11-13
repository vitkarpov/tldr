data "aws_caller_identity" "current" {}

locals {
  # "eu.amazon.nova-lite-v1:0" -> profile prefix "eu", base model "amazon.nova-lite-v1:0".
  model_is_profile = can(regex("^(us|eu|apac|us-gov|global)\\.", var.model_id))
  base_model_id    = local.model_is_profile ? replace(var.model_id, "/^[a-z-]+\\./", "") : var.model_id
}

locals {
  build_dir = "${path.module}/../build"

  common_env = {
    BOT_TOKEN_PARAM      = aws_ssm_parameter.bot_token.name
    ALLOWED_CHAT_IDS     = join(",", var.allowed_chat_ids)
    TABLE_NAME           = aws_dynamodb_table.state.name
    MAX_DURATION_SEC     = tostring(var.max_duration_sec)
    DAILY_LIMIT_PER_CHAT = tostring(var.daily_limit_per_chat)
  }

  max_receive_count = 2
}

# ---------------------------------------------------------------------------
# Secrets
# ---------------------------------------------------------------------------

resource "aws_ssm_parameter" "bot_token" {
  name  = "/${var.name}/telegram-bot-token"
  type  = "SecureString"
  value = var.telegram_bot_token
}

# Telegram sends this in X-Telegram-Bot-Api-Secret-Token; requests without it are rejected.
resource "random_password" "webhook_secret" {
  length  = 64
  special = false
}

# ---------------------------------------------------------------------------
# Storage: S3 (temporary media + transcripts), DynamoDB (quotas + transcript cache)
# ---------------------------------------------------------------------------

resource "aws_s3_bucket" "media" {
  bucket_prefix = "${var.name}-media-"
  force_destroy = true
}

resource "aws_s3_bucket_public_access_block" "media" {
  bucket                  = aws_s3_bucket.media.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "media" {
  bucket = aws_s3_bucket.media.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

# The worker deletes objects after each job; this is the safety net.
resource "aws_s3_bucket_lifecycle_configuration" "media" {
  bucket = aws_s3_bucket.media.id
  rule {
    id     = "expire-everything"
    status = "Enabled"
    filter {}
    expiration {
      days = 1
    }
    abort_incomplete_multipart_upload {
      days_after_initiation = 1
    }
  }
}

resource "aws_dynamodb_table" "state" {
  name         = "${var.name}-state"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"

  attribute {
    name = "pk"
    type = "S"
  }

  ttl {
    attribute_name = "expires_at"
    enabled        = true
  }
}

# ---------------------------------------------------------------------------
# Queue
# ---------------------------------------------------------------------------

resource "aws_sqs_queue" "dlq" {
  name                      = "${var.name}-jobs-dlq"
  message_retention_seconds = 345600 # 4 days
}

resource "aws_sqs_queue" "jobs" {
  name = "${var.name}-jobs"
  # Must be >= worker timeout.
  visibility_timeout_seconds = 360
  # A summary nobody got within an hour is not worth paying for.
  message_retention_seconds = 3600
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.dlq.arn
    maxReceiveCount     = local.max_receive_count
  })
}

# ---------------------------------------------------------------------------
# Lambdas
# ---------------------------------------------------------------------------

data "archive_file" "webhook" {
  type        = "zip"
  source_file = "${local.build_dir}/webhook/bootstrap"
  output_path = "${local.build_dir}/webhook.zip"
}

data "archive_file" "worker" {
  type        = "zip"
  source_file = "${local.build_dir}/worker/bootstrap"
  output_path = "${local.build_dir}/worker.zip"
}

resource "aws_cloudwatch_log_group" "webhook" {
  name              = "/aws/lambda/${var.name}-webhook"
  retention_in_days = var.log_retention_days
}

resource "aws_cloudwatch_log_group" "worker" {
  name              = "/aws/lambda/${var.name}-worker"
  retention_in_days = var.log_retention_days
}

resource "aws_lambda_function" "webhook" {
  function_name    = "${var.name}-webhook"
  role             = aws_iam_role.webhook.arn
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  handler          = "bootstrap"
  filename         = data.archive_file.webhook.output_path
  source_code_hash = data.archive_file.webhook.output_base64sha256
  memory_size      = 128
  timeout          = 10

  environment {
    variables = merge(local.common_env, {
      QUEUE_URL      = aws_sqs_queue.jobs.url
      WEBHOOK_SECRET = random_password.webhook_secret.result
    })
  }

  depends_on = [aws_cloudwatch_log_group.webhook]
}

resource "aws_lambda_function" "worker" {
  function_name    = "${var.name}-worker"
  role             = aws_iam_role.worker.arn
  runtime          = "provided.al2023"
  architectures    = ["arm64"]
  handler          = "bootstrap"
  filename         = data.archive_file.worker.output_path
  source_code_hash = data.archive_file.worker.output_base64sha256
  memory_size      = 512
  timeout          = 300

  environment {
    variables = merge(local.common_env, {
      BUCKET_NAME                 = aws_s3_bucket.media.id
      MODEL_ID                    = var.model_id
      TEMPERATURE                 = tostring(var.temperature)
      SUMMARY_LANGUAGE            = var.summary_language
      TRANSCRIBE_LANGUAGE_OPTIONS = join(",", var.transcribe_language_options)
      TRANSCRIPT_CACHE_TTL_HOURS  = tostring(var.transcript_cache_ttl_hours)
      MAX_RECEIVE_COUNT           = tostring(local.max_receive_count)
    })
  }

  depends_on = [aws_cloudwatch_log_group.worker]
}

resource "aws_lambda_event_source_mapping" "worker" {
  event_source_arn = aws_sqs_queue.jobs.arn
  function_name    = aws_lambda_function.worker.arn
  batch_size       = 1

  scaling_config {
    maximum_concurrency = var.worker_max_concurrency
  }
}

# ---------------------------------------------------------------------------
# API Gateway (HTTP API) -> webhook Lambda
# ---------------------------------------------------------------------------

resource "aws_apigatewayv2_api" "http" {
  name          = "${var.name}-api"
  protocol_type = "HTTP"
}

resource "aws_apigatewayv2_integration" "webhook" {
  api_id                 = aws_apigatewayv2_api.http.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.webhook.invoke_arn
  payload_format_version = "2.0"
}

resource "aws_apigatewayv2_route" "webhook" {
  api_id    = aws_apigatewayv2_api.http.id
  route_key = "POST /telegram/webhook"
  target    = "integrations/${aws_apigatewayv2_integration.webhook.id}"
}

resource "aws_apigatewayv2_stage" "default" {
  api_id      = aws_apigatewayv2_api.http.id
  name        = "$default"
  auto_deploy = true

  # Throttle floods before they reach Lambda.
  default_route_settings {
    throttling_burst_limit = 20
    throttling_rate_limit  = 10
  }
}

resource "aws_lambda_permission" "apigw" {
  statement_id  = "AllowAPIGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.webhook.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.http.execution_arn}/*/*/telegram/webhook"
}

# ---------------------------------------------------------------------------
# Register the webhook with Telegram
# ---------------------------------------------------------------------------

locals {
  webhook_url = "${aws_apigatewayv2_api.http.api_endpoint}/telegram/webhook"
}

resource "terraform_data" "set_webhook" {
  count = var.register_webhook ? 1 : 0

  triggers_replace = [local.webhook_url, "commands-v1", sha256(random_password.webhook_secret.result), sha256(var.telegram_bot_token)]

  provisioner "local-exec" {
    command = <<-EOT
      curl -sS --fail-with-body -X POST "https://api.telegram.org/bot$${TG_TOKEN}/setWebhook" \
        -H 'Content-Type: application/json' \
        -d "{\"url\":\"$${WEBHOOK_URL}\",\"secret_token\":\"$${WEBHOOK_SECRET}\",\"allowed_updates\":[\"message\"],\"drop_pending_updates\":true,\"max_connections\":5}"
      curl -sS --fail-with-body -X POST "https://api.telegram.org/bot$${TG_TOKEN}/setMyCommands" \
        -H 'Content-Type: application/json' \
        --data-binary @- <<'JSON'
      {"commands":[
        {"command":"summary","description":"Ответом на голосовое/кружочек: краткий пересказ с шутками"},
        {"command":"tldr","description":"То же самое, для ленивых"},
        {"command":"chatid","description":"Показать ID чата"},
        {"command":"help","description":"Как мной пользоваться"}
      ]}
      JSON
    EOT
    environment = {
      TG_TOKEN       = var.telegram_bot_token
      WEBHOOK_URL    = local.webhook_url
      WEBHOOK_SECRET = random_password.webhook_secret.result
    }
  }

  depends_on = [aws_apigatewayv2_stage.default, aws_apigatewayv2_route.webhook, aws_lambda_permission.apigw]
}

# ---------------------------------------------------------------------------
# Cost alerts
# ---------------------------------------------------------------------------

resource "aws_budgets_budget" "monthly" {
  count        = var.budget_alert_email == "" ? 0 : 1
  name         = "${var.name}-monthly"
  budget_type  = "COST"
  limit_amount = tostring(var.monthly_budget_usd)
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 80
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.budget_alert_email]
  }

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "FORECASTED"
    subscriber_email_addresses = [var.budget_alert_email]
  }
}

resource "aws_cloudwatch_metric_alarm" "dlq_not_empty" {
  alarm_name          = "${var.name}-dlq-not-empty"
  namespace           = "AWS/SQS"
  metric_name         = "ApproximateNumberOfMessagesVisible"
  dimensions          = { QueueName = aws_sqs_queue.dlq.name }
  statistic           = "Maximum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
}
