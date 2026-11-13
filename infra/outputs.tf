output "webhook_url" {
  value = local.webhook_url
}

output "webhook_secret" {
  value     = random_password.webhook_secret.result
  sensitive = true
}

output "worker_log_group" {
  value = aws_cloudwatch_log_group.worker.name
}

output "webhook_log_group" {
  value = aws_cloudwatch_log_group.webhook.name
}

output "dlq_url" {
  value = aws_sqs_queue.dlq.url
}
