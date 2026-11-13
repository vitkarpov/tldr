# tldr

[@bfeatb_bot](https://t.me/bfeatb_bot), a Telegram bot that summarizes voice messages, round videos (кружочки), audio and video in a funny way.
It runs on AWS: Amazon Transcribe turns speech into text and Amazon Nova Lite on Bedrock writes the summary. Everything the bot says in chat is in Russian.

```
Telegram ──webhook──▶ API Gateway ──▶ λ webhook ──SQS──▶ λ worker ──▶ Telegram reply
                                       │  secret check     │ download file → S3
                                       │  chat whitelist   │ Amazon Transcribe (batch)
                                       │  daily quota      │ Nova Lite on Bedrock (Converse) 
                                       ▼                   ▼
                                    DynamoDB (quotas, transcript cache)
```

## Usage

- In a group, reply to a voice or video message with `/summary` or `/tldr`.
- Add a style if you like: `/summary как спортивный комментатор`.
- Reply to any post (text, photo with caption, forwarded channel post, voice/video) with `/roast` (or `/r`) to get a short, witty "we're all in this together" roast. The bot sees the image too (Nova Lite is multimodal). With privacy mode on the bot can't read the rest of the chat, so give it context: `/roast автор продал компанию Яндексу за миллиарды`.
- In a DM with the bot, send or forward a voice message and it summarizes it straight away.
- `/chatid` shows the chat ID. It works in any chat so you can fill in the whitelist.

## Cost guards

| Guard | Where | Default |
|---|---|---|
| Webhook secret header (`X-Telegram-Bot-Api-Secret-Token`) | webhook | random 64 chars |
| **Chat whitelist**, fail-closed (empty = nobody) | webhook **and** worker | `[]` |
| Daily summaries per chat | DynamoDB atomic counter | 30 |
| Max media duration | webhook + worker | 10 min |
| Transcript cache (re-summarizing doesn't pay Transcribe again) | DynamoDB | 72 h |
| Max concurrent workers | SQS event source | 2 |
| API Gateway throttling | stage | 10 rps / burst 20 |
| Retries | SQS → DLQ after 2 attempts, stale jobs dropped after 1 h | |
| Monthly AWS Budget email (optional) | `budget_alert_email` | off |

## Deploy

Prerequisites: Go, Terraform ≥ 1.6, `curl`, and the `webkwizards` AWS profile (`aws sso login --profile webkwizards`). Terraform and `make logs` use that profile; override it with `aws_profile` / `AWS_PROFILE`.

1. **Create the bot** in [@BotFather](https://t.me/BotFather) with `/newbot` and copy the token. Leave privacy mode **on**: the bot still gets `/commands` and doesn't receive anything else from the group.
2. **Model access:** Amazon Nova models need no use-case form. If the first call returns `AccessDeniedException`, open the Nova Lite page in the [Bedrock console](https://console.aws.amazon.com/bedrock/) for your region (default `eu-west-1`) and enable it.
3. Configure:
   ```sh
   cp infra/terraform.tfvars.example infra/terraform.tfvars   # set telegram_bot_token
   ```
4. Deploy. This builds the Go Lambdas (linux/arm64), applies Terraform and calls `setWebhook` + `setMyCommands`:
   ```sh
   make deploy
   ```
5. Add the bot to your group and send `/chatid`. Put the ID into `allowed_chat_ids` (plus your own user ID if you want to use it in a DM), then run `make deploy` again.

Logs: `make logs` (worker) or `make logs FN=webhook`. Messages from chats that aren't whitelisted are logged with their `chat_id`.

## Configuration (`infra/terraform.tfvars`)

| Variable | Default | |
|---|---|---|
| `model_id` | `eu.amazon.nova-lite-v1:0` | Bedrock model / inference profile (Converse API) |
| `temperature` | `0.7` | higher = wilder jokes |
| `summary_language` | `Russian` | `auto` = same language as the message |
| `transcribe_language_options` | `[]` (auto-detect) | e.g. `["ru-RU","en-US"]`, which improves language detection |
| `daily_limit_per_chat`, `max_duration_sec`, `transcript_cache_ttl_hours`, `worker_max_concurrency` | see above | |

To change the bot's chat messages, edit `internal/texts/texts.go`. To change the humor style, edit the system prompts (`systemPrompt`, `roastPrompt`) in `internal/summarize/summarize.go`.

## Rough cost per summary

Most of the cost is Amazon Transcribe, billed per minute of audio (see [pricing](https://aws.amazon.com/transcribe/pricing/)). The Nova Lite call is a couple of thousand tokens, a small fraction of a cent at [Bedrock pricing](https://aws.amazon.com/bedrock/pricing/). Lambda, SQS, DynamoDB and API Gateway are effectively free at group-chat volume.

## Notes

- AWS resources are named with the `tg-summary` prefix (`infra/variables.tf`), from before the project was renamed to tldr. Changing it recreates every resource, including the webhook URL, so it was left as is.
- Telegram only lets bots download files up to 20 MB.
- Media and transcripts are deleted from S3 after each job; a 1-day lifecycle rule is the safety net. Cached transcripts expire from DynamoDB via TTL.
- The Terraform state contains the bot token. Use the encrypted S3 backend (commented out in `infra/versions.tf`) if the state leaves your laptop.
- If the worker gets `AccessDeniedException` from Bedrock, check model access (step 2) and the `bedrock:InvokeModel` statement in `infra/iam.tf`.

## Layout

```
cmd/webhook      API Gateway handler: auth, whitelist, quota, enqueue
cmd/worker       SQS consumer: download → Transcribe → Nova Lite → reply
internal/        config, telegram client, transcribe, summarize, store (DynamoDB), texts (RU strings)
infra/           Terraform
```
