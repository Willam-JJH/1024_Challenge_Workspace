# 旧接口清单（功能参照）

共 267 个端点，从原 Unscripted 仓库提取，仅作功能对照。
新接口按 `docs/api/conventions.md` 的规范重新设计，路径不一一对应。

## admin（26）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/admin/audit` | 公开 | The audit log (ADM-11), newest first |
| GET | `/admin/audit/actions` | 公开 | Every action verb in the log (filter menu) |
| GET | `/admin/companies` | 公开 | Companies of the current event (rosters, captains, projects) |
| GET | `/admin/companies/:companyId` | 公开 | A company’s file: members, invitations, project, roster log |
| POST | `/admin/companies/:companyId/dissolve` | 公开 | Force-dissolve a company (members removed, project hidden, votes returned) |
| PUT | `/admin/companies/:companyId/roster-lock` | 公开 | Lock or unlock one company’s roster (ADM-3) |
| POST | `/admin/companies/:companyId/transfer-captain` | 公开 | Make another active member the captain (the submitter follows the old captain) |
| POST | `/admin/companies/:companyId/transfer-submitter` | 公开 | Hand the project submitter role to another active member |
| GET | `/admin/dashboard` | 公开 | Event vital signs (ADM-1); sections outside the viewer’s role are null |
| POST | `/admin/members/move` | 公开 | Move a person to another company, or remove them (toCompanyId = null) |
| GET | `/admin/search` | 公开 | Command bar: people, companies and projects |
| GET | `/admin/users` | 公开 | Search users (email, English or display name) with filters and sort/dir |
| POST | `/admin/users` | 公开 | Create an account (pending) and email an invitation to set its password |
| GET | `/admin/users/:userId` | 公开 | A person’s file: profile, teams, project, votes, keys, usage, sessions |
| PUT | `/admin/users/:userId/locale` | 公开 | Set the email language of one account (audited) |
| PUT | `/admin/users/:userId/mode` | 公开 | Change how someone takes part (online, in person, both); audited |
| POST | `/admin/users/:userId/remove` | 公开 | Remove an account: keep its data, also leave its company, or delete the row entirely |
| POST | `/admin/users/:userId/resend-activation` | 公开 | Re-send a pending teammate |
| PUT | `/admin/users/:userId/role` | 公开 | Change a global role (admin rights need a super admin; never your own) |
| POST | `/admin/users/:userId/sign-out` | 公开 | Sign someone out of every session and connected app (force logout) |
| POST | `/admin/users/:userId/suspend` | 公开 | Suspend an account: signed out everywhere, LLM off, coins stop counting |
| GET | `/admin/users/:userId/teams` | 公开 | A user |
| POST | `/admin/users/:userId/unsuspend` | 公开 | Reinstate a suspended account |
| GET | `/admin/users/export` | 公开 | Everyone matching the filters as CSV (audited) |
| PUT | `/admin/users/locale` | 公开 | Set the email language for everyone matching the filters (audited) |
| POST | `/admin/users/remove` | 公开 | Remove the ticked accounts (or everyone matching): audited one by one |

## admin-email（1）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/admin/email/quota` | 公开 | The email provider |

## admin-llm（22）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/api-keys` | 公开 | List anyone |
| POST | `/api-keys/:id/revoke` | 公开 | Revoke anyone |
| GET | `/budget` | 公开 | The event budget |
| PUT | `/budget` | 公开 | Set the event budget (micro-USD) |
| GET | `/kill-switches` | 公开 | Kill switch state |
| PUT | `/kill-switches/global` | 公开 | Turn the whole LLM API on or off |
| PUT | `/kill-switches/models/:modelId` | 公开 | Turn one model on or off |
| PUT | `/kill-switches/users/:userId` | 公开 | Turn one person |
| GET | `/models` | 公开 | List models with prices and routes |
| POST | `/models` | 公开 | Add a public model |
| DELETE | `/models/:id` | 公开 | Delete a model |
| PATCH | `/models/:id` | 公开 | Edit a model (prices, limits, capabilities, enabled) |
| POST | `/models/:id/routes` | 公开 | Map the model to an upstream model (fallback by priority) |
| GET | `/overview` | 公开 | Spend, remaining credit, burn rate, forecast and breakdowns |
| DELETE | `/routes/:id` | 公开 | Delete a route |
| PATCH | `/routes/:id` | 公开 | Edit a route |
| GET | `/upstreams` | 公开 | List upstreams (keys masked) |
| POST | `/upstreams` | 公开 | Add an OpenAI-compatible upstream (the key is write-only) |
| DELETE | `/upstreams/:id` | 公开 | Delete an upstream (and its routes) |
| PATCH | `/upstreams/:id` | 公开 | Edit an upstream; send apiKey to replace the key (required when the host or extra headers change) |
| GET | `/usage/teams` | 公开 | Burn rate: companies by AI tokens through the gateway (WP15, Highest Burn Rate) |
| GET | `/usage/users` | 公开 | Usage per user (most expensive first) |

## announcements（10）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/admin/announcements` | 公开 | Every announcement |
| POST | `/admin/announcements` | 公开 | Write an announcement (publish now or keep as a draft) |
| DELETE | `/admin/announcements/:announcementId` | 公开 | Delete an announcement |
| PATCH | `/admin/announcements/:announcementId` | 公开 | Edit an announcement |
| GET | `/admin/sponsors` | 公开 | The sponsors strip |
| POST | `/admin/sponsors` | 公开 | Add a sponsor (logo: a `sponsor_logo` upload) |
| DELETE | `/admin/sponsors/:sponsorId` | 公开 | Remove a sponsor |
| PATCH | `/admin/sponsors/:sponsorId` | 公开 | Edit a sponsor |
| PUT | `/admin/sponsors/order` | 公开 | Reorder the strip (every sponsor id, in order) |
| GET | `/announcements` | 公开 | Live announcements (the banner and the news feed), newest first |

## api-keys（3）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/api-keys` | 登录 | Create an API key. The full key is returned ONCE. |
| POST | `/api-keys/:id/rotate` | 登录 | Rotate an API key: revoke it and issue a new one with the same label |
| GET | `/llm/setup` | 登录 | Gateway base URL and public models (for setup snippets) |

## auth（9）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/auth/bot/challenge` | 公开 | Issue a self-hosted proof-of-work challenge |
| POST | `/auth/login` | 公开 | Sign in with email + password |
| POST | `/auth/logout` | 公开 | Sign out of this device |
| POST | `/auth/logout-everywhere` | 登录 | Sign out on every device |
| POST | `/auth/password/forgot` | 公开 | Email a password-reset code |
| POST | `/auth/password/reset` | 公开 | Set a new password (signs in, revokes every other session) |
| POST | `/auth/password/verify` | 公开 | Exchange a reset code for a reset token |
| GET | `/auth/sessions` | 登录 | My active sessions |
| DELETE | `/auth/sessions/:sessionId` | 登录 | Revoke one session |

## csp-report（1）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/csp-report` | 公开 | Content-Security-Policy violation reports from browsers (logged as metadata) |

## email-admin（14）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/admin/email/audience/estimate` | 公开 | How many people an audience reaches (by language), before the daily cap |
| GET | `/admin/email/broadcasts` | 公开 | Broadcasts, newest first |
| POST | `/admin/email/broadcasts` | 公开 | Save a broadcast draft |
| DELETE | `/admin/email/broadcasts/:broadcastId` | 公开 | Delete a draft (or cancelled) broadcast |
| PATCH | `/admin/email/broadcasts/:broadcastId` | 公开 | Edit a draft or scheduled broadcast |
| POST | `/admin/email/broadcasts/:broadcastId/cancel` | 公开 | Cancel a draft or scheduled broadcast |
| POST | `/admin/email/broadcasts/:broadcastId/schedule` | 公开 | Send now (scheduledAt null) or at a future time |
| POST | `/admin/email/broadcasts/:broadcastId/test` | 公开 | Send the broadcast to myself |
| POST | `/admin/email/broadcasts/preview` | 公开 | Render broadcast content as the recipients will see it |
| GET | `/admin/email/messages` | 公开 | The email log (newest first) with counts by status |
| POST | `/admin/email/messages/:messageId/resend` | 公开 | Queue a failed message again (broadcasts and offline-session emails) |
| POST | `/admin/email/preview` | 公开 | Render a template with sample props in English or Chinese |
| GET | `/admin/email/setup` | 公开 | The email driver, and whether the outbox reports a daily quota |
| GET | `/admin/email/templates` | 公开 | Every email template |

## email-preferences（2）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/email/unsubscribe` | 公开 | Turn optional emails off with the token from an email (no sign-in) |
| POST | `/email/unsubscribe/one-click` | 公开 | RFC 8058 one-click unsubscribe (List-Unsubscribe-Post) |

## event-admin（7）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/admin/event` | 公开 | The current event: schedule, stage, switches |
| PATCH | `/admin/event/schedule` | 公开 | Edit stage dates (UTC instants; order checked) and the stage override |
| PUT | `/admin/event/switches` | 公开 | Open/close registration, lock every project or every roster |
| GET | `/admin/feature-flags` | 公开 | Feature flags |
| POST | `/admin/feature-flags` | 公开 | Create a feature flag |
| DELETE | `/admin/feature-flags/:flagId` | 公开 | Delete a feature flag |
| PATCH | `/admin/feature-flags/:flagId` | 公开 | Turn a flag on or off, or describe it |

## exhibition（24）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| PUT | `/exhibition/assignments/:assignmentId/intro` | 登录 | Write or edit the award-day introduction, up to ~150 words (members of the drawing company) |
| GET | `/exhibition/collaborations` | 公开 | Published collaborations (announcements), newest first |
| POST | `/exhibition/collaborations` | 登录 | Propose a collaboration or pretend acquisition to another company |
| DELETE | `/exhibition/comments/:commentId` | 登录 | Delete my comment (replies keep a |
| PATCH | `/exhibition/comments/:commentId` | 登录 | Edit my comment |
| POST | `/exhibition/comments/:commentId/report` | 登录 | Report a comment to the organisers (once per person) |
| GET | `/exhibition/companies/:companyId/visits` | 登录 | Who stamped this company, with their notes (members and admins) |
| GET | `/exhibition/feed` | 公开 | Activity ticker: new projects, collaborations, intros and ads (public) |
| DELETE | `/exhibition/intros/:introId/reply` | 登录 | Delete our reply |
| PUT | `/exhibition/intros/:introId/reply` | 登录 | The introduced company |
| GET | `/exhibition/projects/:projectId/comments` | 公开 | Comment threads on a project, oldest first (public) |
| POST | `/exhibition/projects/:projectId/comments` | 登录 | Comment (or reply to a top-level comment) |
| GET | `/exhibition/promotions` | 公开 | The ad wall / ticker (public) |
| POST | `/exhibition/promotions` | 登录 | Post an ad to the wall or ticker (members; 3 live per company) |
| DELETE | `/exhibition/promotions/:promotionId` | 登录 | Take my company |
| PATCH | `/exhibition/promotions/:promotionId` | 登录 | Edit my company |
| DELETE | `/exhibition/visits/:projectId` | 登录 | Remove a passport stamp |
| PUT | `/exhibition/visits/:projectId` | 登录 |  |
| GET | `/me/assignment` | 登录 | My company |
| GET | `/me/collaborations` | 登录 | My company |
| GET | `/me/digest-preferences` | 登录 | Email digest preferences |
| PUT | `/me/digest-preferences` | 登录 | Turn the daily comment digest on or off |
| GET | `/me/passport` | 登录 | My Visitor Passport: stamps and progress toward 3 companies (never a gate) |
| GET | `/me/promotions` | 登录 | My company |

## exhibition-admin（17）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/admin/exhibition` | 公开 | Exhibition at a glance: passports, intros, comments, deals, ads |
| GET | `/admin/exhibition/assignments` | 公开 | Assignments: live ones, eligible companies, gaps, skipped companies |
| PATCH | `/admin/exhibition/assignments/:assignmentId` | 公开 | Adjust a draft assignment |
| POST | `/admin/exhibition/assignments/:assignmentId/cancel` | 公开 | Cancel an assignment |
| POST | `/admin/exhibition/assignments/:assignmentId/given` | 公开 | Mark an introduction as given (public, creators see who) or take it back |
| POST | `/admin/exhibition/assignments/:assignmentId/reassign` | 公开 | Give a company another project to introduce (the old one is unavailable) |
| POST | `/admin/exhibition/assignments/assign-remaining` | 公开 | Draft assignments for eligible companies that have none (late joiners) |
| POST | `/admin/exhibition/assignments/generate` | 公开 | Draw a random derangement over eligible companies (drafts, replaces drafts) |
| POST | `/admin/exhibition/assignments/publish` | 公开 | Publish every draft and notify the companies (in-app + email) |
| POST | `/admin/exhibition/assignments/reset` | 公开 | Cancel the whole draw (drafts dropped, published ones cancelled) to draw again |
| POST | `/admin/exhibition/assignments/reveal-all` | 公开 | Mark every published introduction as given at once |
| GET | `/admin/exhibition/collaborations` | 公开 | Every published collaboration of the event, hidden ones included |
| GET | `/admin/exhibition/comments` | 公开 | Comments to moderate (reported first) |
| POST | `/admin/exhibition/intros/:introId/moderate` | 公开 | Hide or show an introduction |
| PUT | `/admin/exhibition/presence/:companyId` | 公开 | Mark a company present (or not) on award day; only present teams are drawn |
| GET | `/admin/exhibition/promotions` | 公开 | Every ad of the event |
| POST | `/admin/exhibition/replies/:replyId/moderate` | 公开 | Hide or show a reply to an introduction |

## gateway（6）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/chat/completions` | 公开 |  |
| POST | `/embeddings` | 公开 |  |
| GET | `/health` | 公开 |  |
| GET | `/models` | 公开 |  |
| POST | `/responses` | 公开 |  |
| POST | `/search` | 公开 |  |

## github（1）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/github/repos/:owner/:repo` | 登录 | GitHub repo card (cached; status not_found = missing or not public) |

## health（1）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/health` | 公开 | Service health |

## link-preview（1）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/link-preview` | 登录 | Preview a link (title, description, og:image) for a link card |

## me（15）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/me` | 公开 | The signed-in person |
| GET | `/me/company` | 公开 | My company in the current event (member view) |
| DELETE | `/me/deletion-request` | 公开 | Cancel my deletion request |
| POST | `/me/deletion-request` | 公开 | Ask for my account to be deleted (SEC-6) |
| GET | `/me/invites` | 公开 | Invitations addressed to me |
| POST | `/me/invites/:inviteId/accept` | 公开 | Accept an invitation |
| POST | `/me/invites/:inviteId/decline` | 公开 | Decline an invitation |
| GET | `/me/join-requests` | 公开 | My requests to join companies |
| DELETE | `/me/join-requests/:requestId` | 公开 | Withdraw a join request |
| PUT | `/me/locale` | 公开 | Set my language (emails + website cookie) |
| GET | `/me/notifications` | 公开 | My notifications (newest first) |
| POST | `/me/notifications/read` | 公开 | Mark notifications read |
| POST | `/me/password` | 公开 | Change my password (signs out every other session and every OAuth app) |
| PATCH | `/me/profile` | 公开 | Update my profile |
| PUT | `/me/registration` | 公开 | Switch audience ↔ competitor, or register for the current event |

## media（10）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/admin/media/:assetId/remove` | 登录 | Take a file down (any purpose, even if used): bytes deleted, audited |
| GET | `/files/:assetId` | 公开 |  |
| GET | `/files/:assetId/:variant` | 公开 |  |
| DELETE | `/media/:assetId` | 登录 | Delete an upload that no draft or version uses |
| GET | `/media/:assetId` | 公开 | Asset metadata (public assets for anyone; drafts for members) |
| PATCH | `/media/:assetId` | 登录 | Set alt text |
| POST | `/media/:assetId/complete` | 登录 | Finish an upload: sniff the type, re-encode images (EXIF stripped) |
| PUT | `/media/:assetId/parts/:part` | 登录 | Upload one part of a large file (local storage driver only) |
| PUT | `/media/:assetId/upload` | 登录 | Upload the bytes (local storage driver only) |
| POST | `/media/uploads` | 登录 | Reserve an upload and get the URL to PUT the bytes to |

## oauth（7）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/.well-known/oauth-authorization-server` | 公开 | Authorization server metadata (RFC 8414) |
| GET | `/authorize` | 公开 | Authorization endpoint (Authorization Code + PKCE S256). Always redirects. |
| GET | `/consent` | 登录 | What the consent screen shows for an authorization request |
| POST | `/consent` | 登录 | Allow or deny an app; returns where to send the browser (the app) |
| POST | `/revoke` | 公开 | Token revocation (RFC 7009): signs the device out (its whole token family) |
| POST | `/token` | 公开 | Token endpoint: authorization_code (PKCE) and refresh_token (rotating) |
| GET | `/userinfo` | 公开 | Who the access token belongs to (needs the profile scope) |

## oauth-connected-apps（3）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/me/connected-apps` | 登录 | Apps I signed in to with my account (Intern), with their devices |
| DELETE | `/me/connected-apps/:clientId` | 登录 | Revoke an app: sign out all its devices and forget the approval |
| DELETE | `/me/connected-apps/:clientId/devices/:deviceId` | 登录 | Sign one device out of an app |

## offline（11）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/admin/offline-sessions` | 公开 | Offline sessions with answer counts |
| POST | `/admin/offline-sessions` | 公开 | Create an offline session |
| DELETE | `/admin/offline-sessions/:sessionId` | 公开 | Delete a session nobody answered yet |
| PATCH | `/admin/offline-sessions/:sessionId` | 公开 | Edit a session (more seats promote the waitlist; a new time re-arms reminders) |
| POST | `/admin/offline-sessions/:sessionId/cancel` | 公开 | Cancel a session and tell everyone who answered |
| GET | `/admin/offline-sessions/:sessionId/rsvps` | 公开 | Everyone who answered, with waitlist places and check-ins |
| PUT | `/admin/offline-sessions/:sessionId/rsvps/:rsvpId/check-in` | 公开 | Tick someone off at the door (or undo it) |
| GET | `/offline-sessions` | 公开 | Offline sessions of the current event (with my answer when signed in) |
| GET | `/offline-sessions/:sessionId` | 公开 | One offline session |
| GET | `/offline-sessions/:sessionId/ics` | 公开 | The session as an iCalendar file |
| PUT | `/offline-sessions/:sessionId/rsvp` | 登录 | Answer going / maybe / not going (full → waitlist) |

## pitch（12）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| PATCH | `/admin/pitch` | 公开 | The stream title and the on-air switch |
| GET | `/admin/pitch/messages` | 公开 | The latest live-page messages, hidden ones included |
| POST | `/admin/pitch/messages/:messageId/moderate` | 公开 | Hide a live-page message, or put it back |
| GET | `/admin/pitch/sources` | 公开 | Every stream source of the event, in order |
| POST | `/admin/pitch/sources` | 公开 | Add a source (Bilibili room, YouTube link or Twitch channel) |
| DELETE | `/admin/pitch/sources/:sourceId` | 公开 | Remove a source |
| PATCH | `/admin/pitch/sources/:sourceId` | 公开 | Change a source: address, name, region, enabled |
| POST | `/admin/pitch/sources/:sourceId/move` | 公开 | Move a source one place up or down |
| GET | `/pitch` | 公开 | The Pitching Day page: on air or not, the sources, the match, the chat |
| POST | `/pitch/messages` | 登录 | Say something on the live page |
| DELETE | `/pitch/messages/:messageId` | 登录 | Take my own message back |
| GET | `/pitch/stream` | 公开 | Live page updates over SSE |

## projects（14）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/me/project` | 登录 | My company |
| GET | `/projects` | 公开 | Gallery of published projects (EXH-1) |
| GET | `/projects/:projectId` | 登录 | Draft view (members and admins) |
| POST | `/projects/:projectId/approve-agent-draft` | 登录 | Mark an agent-written draft as reviewed by a human |
| PATCH | `/projects/:projectId/draft` | 登录 | Save the draft (submitter; needs the edit lease and the current revision) |
| POST | `/projects/:projectId/lock` | 登录 | Take the edit lease (PRJ-4); edit_locked when another tab/device holds it |
| POST | `/projects/:projectId/lock/heartbeat` | 登录 | Keep the edit lease |
| POST | `/projects/:projectId/lock/release` | 登录 | Give the edit lease back (no-op if it is no longer yours) |
| POST | `/projects/:projectId/publish` | 登录 | Turn in / update the project: snapshot the draft as a new live version |
| GET | `/projects/:projectId/versions` | 登录 | Published versions, newest first (PRJ-3) |
| GET | `/projects/:projectId/versions/:version` | 登录 | One published version |
| POST | `/projects/:projectId/versions/:version/restore` | 登录 | Copy a published version into the draft (publish again to make it live) |
| GET | `/projects/by-company/:companySlug` | 公开 | A published project page (members, repo card, media) |
| GET | `/projects/draft-schema` | 公开 | JSON Schema of the draft fields an agent may send (CLI-12) |

## projects-admin（4）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/admin/projects` | 公开 | All projects of the current event, drafts included |
| GET | `/admin/projects/:projectId` | 公开 | A project |
| GET | `/admin/projects/:projectId/versions` | 公开 | A project |
| GET | `/admin/projects/:projectId/versions/:version` | 公开 | One published version |

## public（3）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/public/event` | 公开 | Current event: stage, schedule, names, flags and sponsors |
| GET | `/public/shortlist` | 公开 | The announced shortlist (a Distinction each), in the organisers |
| GET | `/public/stats` | 公开 | Aggregate counters for the landing page |

## registration（6）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/auth/activation/complete` | 公开 | Activate a pre-created teammate account and join the team |
| POST | `/auth/activation/preview` | 公开 | Look at a teammate activation link before using it |
| POST | `/auth/register` | 公开 | Register (REG-1..4) and receive an email code |
| POST | `/auth/register/resend` | 公开 | Send a new registration code |
| POST | `/auth/register/verify` | 公开 | Confirm the email code: activates the account and signs in |
| POST | `/me/registration` | 登录 | Join the event as an audience member, a competitor or a company founder |

## results（3）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/judging/projects` | 登录 | Projects to judge, each with my own score card |
| PUT | `/judging/scores/:projectId` | 登录 | Save my scores (1–10) and private notes; submit when final |
| GET | `/results` | 公开 | Published results: awards with winners, and the final board |

## round2（1）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| PUT | `/projects/:projectId/round2/attendance` | 公开 | Answer the attendance question for this project (submitter or captain) |

## teams（18）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| POST | `/companies` | 登录 | Found a company (you become captain) |
| PATCH | `/companies/:companyId` | 登录 | Edit the company profile (any member) |
| DELETE | `/companies/:companyId/invite-link` | 登录 | Disable the shareable invite link (captain) |
| POST | `/companies/:companyId/invite-link` | 登录 | Create a new shareable invite link (captain) |
| POST | `/companies/:companyId/invites` | 登录 | Invite teammates (captain) |
| DELETE | `/companies/:companyId/invites/:inviteId` | 登录 | Revoke an invitation (captain) |
| POST | `/companies/:companyId/invites/:inviteId/resend` | 登录 | Re-send an invitation / activation link (captain or admin) |
| GET | `/companies/:companyId/join-requests` | 登录 | Pending join requests (members) |
| POST | `/companies/:companyId/join-requests` | 登录 | Ask to join a recruiting company |
| POST | `/companies/:companyId/leave` | 登录 | Quit the company |
| POST | `/companies/:companyId/transfer-captain` | 登录 | Hand the captaincy to another member (captain) |
| POST | `/companies/:companyId/transfer-submitter` | 登录 | Appoint the project submitter (the captain, who is the submitter by default) |
| GET | `/companies/:companyKey` | 公开 | Public company profile (by id or slug) |
| GET | `/finder/companies` | 登录 | Teammate finder: recruiting companies with free seats (sort/dir order them) |
| GET | `/finder/people` | 登录 | Teammate finder: individuals looking for a team (sort/dir order them) |
| POST | `/invites/accept` | 登录 | Join a company through an invitation link |
| POST | `/invites/decline` | 登录 | Decline an email invitation |
| POST | `/invites/preview` | 公开 | Look at an invitation link (signed in or not) |

## usage（3）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/usage/requests` | 登录 | My recent LLM requests (numbers only, no content) |
| GET | `/usage/summary` | 登录 | My LLM usage: totals, per event-time day, per model, per client |
| GET | `/usage/team` | 登录 | My company |

## votes（7）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/market` | 公开 | The live board: totals per company, shared ranks, last change (public) |
| GET | `/market/big-screen` | 公开 | Big-Screen ceremony view: top N with previous rank and coin deltas |
| GET | `/market/stream` | 公开 | Server-Sent Events: the board (or Big Screen) live |
| GET | `/me/wallet` | 登录 | My coins: invested picks, coins left, |
| PUT | `/me/wallet` | 登录 | Save the whole ballot at once (at most 3 companies, atomic) |
| POST | `/me/wallet/coins` | 登录 | Invest one coin in a company (idempotent) |
| DELETE | `/me/wallet/coins/:companyId` | 登录 | Withdraw the coin invested in a company (idempotent) |

## votes-admin（5）

| 方法 | 路径 | 鉴权 | 摘要 |
|---|---|---|---|
| GET | `/admin/voting` | 公开 | Voting state, visibility, ballot statistics and the live totals |
| GET | `/admin/voting/ballots` | 公开 | Who voted for whom (admins only, never public) |
| GET | `/admin/voting/events` | 公开 | Every cast, withdrawal, return and void (VOTE-8) |
| PUT | `/admin/voting/visibility` | 公开 | Show or hide live totals from the public (admins always see them) |
| POST | `/admin/voting/void` | 公开 | Void one coin or a voter |

