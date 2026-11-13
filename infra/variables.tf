variable "name" {
  description = "Name prefix for all resources."
  type        = string
  default     = "tg-summary"
}

variable "aws_profile" {
  description = "AWS CLI profile Terraform deploys with."
  type        = string
  default     = "webkwizards"
}

variable "aws_region" {
  description = "AWS region. Must offer Amazon Transcribe and the Bedrock model in model_id."
  type        = string
  default     = "eu-west-1"
}

variable "telegram_bot_token" {
  description = "Bot token from @BotFather. Stored in SSM Parameter Store as a SecureString."
  type        = string
  sensitive   = true
}

variable "allowed_chat_ids" {
  description = "Whitelist of Telegram chat IDs allowed to use the bot. Empty = nobody (fail closed). Group IDs are negative, e.g. -1001234567890."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for id in var.allowed_chat_ids : can(regex("^-?[0-9]+$", id))])
    error_message = "allowed_chat_ids must contain numeric chat IDs only."
  }
}

variable "model_id" {
  description = "Bedrock model or cross-region inference profile ID used via the Converse API. In eu-west-1, Nova Lite is only available through the EU inference profile."
  type        = string
  default     = "eu.amazon.nova-lite-v1:0"
}

variable "roast_model_id" {
  description = "Bedrock model or inference profile for /roast. Must accept images. Claude is much better than Nova at jokes, especially in Russian."
  type        = string
  default     = "eu.anthropic.claude-sonnet-4-6"
}

variable "temperature" {
  description = "Sampling temperature (0-1). A bit of randomness keeps the jokes fresh."
  type        = number
  default     = 0.7
}

variable "summary_language" {
  description = "Language of the summaries (e.g. \"Russian\"). \"auto\" = same language as the message."
  type        = string
  default     = "Russian"
}

variable "transcribe_language_options" {
  description = "Amazon Transcribe language codes the messages may be in, e.g. [\"en-US\", \"de-DE\"]. Empty = auto-detect from all languages; one entry = fixed language. Listing 2+ likely languages improves accuracy."
  type        = list(string)
  default     = []
}

variable "max_duration_sec" {
  description = "Longest voice/video message the bot will process (cost guard)."
  type        = number
  default     = 600
}

variable "daily_limit_per_chat" {
  description = "Max summaries per chat per UTC day (cost guard)."
  type        = number
  default     = 30
}

variable "transcript_cache_ttl_hours" {
  description = "How long transcripts are cached (so re-summarizing the same message doesn't pay Transcribe again)."
  type        = number
  default     = 72
}

variable "worker_max_concurrency" {
  description = "Max concurrent worker Lambdas (SQS event source max concurrency, min 2)."
  type        = number
  default     = 2
}

variable "log_retention_days" {
  type    = number
  default = 14
}

variable "budget_alert_email" {
  description = "If set, creates an account-wide monthly AWS Budget that emails this address at 80% actual / 100% forecasted of monthly_budget_usd."
  type        = string
  default     = ""
}

variable "monthly_budget_usd" {
  type    = number
  default = 10
}

variable "register_webhook" {
  description = "Call Telegram setWebhook from terraform apply (needs curl locally)."
  type        = bool
  default     = true
}
