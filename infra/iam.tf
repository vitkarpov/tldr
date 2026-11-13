data "aws_iam_policy_document" "lambda_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["lambda.amazonaws.com"]
    }
  }
}

# --- webhook ---------------------------------------------------------------

resource "aws_iam_role" "webhook" {
  name               = "${var.name}-webhook"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

data "aws_iam_policy_document" "webhook" {
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.webhook.arn}:*"]
  }
  statement {
    sid       = "BotToken"
    actions   = ["ssm:GetParameter"]
    resources = [aws_ssm_parameter.bot_token.arn]
  }
  statement {
    sid       = "Enqueue"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.jobs.arn]
  }
  statement {
    sid       = "Quota"
    actions   = ["dynamodb:UpdateItem"]
    resources = [aws_dynamodb_table.state.arn]
  }
}

resource "aws_iam_role_policy" "webhook" {
  role   = aws_iam_role.webhook.id
  policy = data.aws_iam_policy_document.webhook.json
}

# --- worker ----------------------------------------------------------------

resource "aws_iam_role" "worker" {
  name               = "${var.name}-worker"
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

data "aws_iam_policy_document" "worker" {
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.worker.arn}:*"]
  }
  statement {
    sid       = "BotToken"
    actions   = ["ssm:GetParameter"]
    resources = [aws_ssm_parameter.bot_token.arn]
  }
  statement {
    sid       = "Queue"
    actions   = ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"]
    resources = [aws_sqs_queue.jobs.arn]
  }
  statement {
    sid       = "State"
    actions   = ["dynamodb:GetItem", "dynamodb:PutItem"]
    resources = [aws_dynamodb_table.state.arn]
  }
  # Batch Transcribe reads the input and writes the output with the caller's permissions.
  statement {
    sid       = "Media"
    actions   = ["s3:PutObject", "s3:GetObject", "s3:DeleteObject"]
    resources = ["${aws_s3_bucket.media.arn}/*"]
  }
  statement {
    sid       = "MediaList"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.media.arn]
  }
  statement {
    sid = "Transcribe"
    actions = [
      "transcribe:StartTranscriptionJob",
      "transcribe:GetTranscriptionJob",
      "transcribe:DeleteTranscriptionJob",
    ]
    resources = ["*"]
  }
  # Bedrock Converse = bedrock:InvokeModel. A cross-region inference profile needs
  # access to the profile itself and to the model in every region it routes to.
  statement {
    sid     = "Bedrock"
    actions = ["bedrock:InvokeModel"]
    resources = flatten([for id, m in local.bedrock_models : concat(
      ["arn:aws:bedrock:*::foundation-model/${m.base_id}"],
      m.is_profile ? ["arn:aws:bedrock:${var.aws_region}:${data.aws_caller_identity.current.account_id}:inference-profile/${id}"] : [],
    )])
  }
}

resource "aws_iam_role_policy" "worker" {
  role   = aws_iam_role.worker.id
  policy = data.aws_iam_policy_document.worker.json
}
