# 赛季生命周期

> 状态：**已实现**（2026-11-17）。这一页定义平台从「一次性活动」转向「每年一届」之后，
> 一届比赛怎么开始、怎么结束、结束之后数据变成什么样。

---

## 0 一句话

一届比赛（`events` 里的一行）经历 **赛前 → 进行中 → 结束后保留期 → 收口** 四个阶段。
收口是一个时刻，由后台任务自动写入 `events.archived_at`；从那一刻起，这一届的作品进入
**前序作品**，报名与投票关闭，非管理员的参赛身份回落为**普通用户**。

---

## 1 时间轴

```
starts_at                    ends_at                    ends_at + 保留期（默认 30 天）
    │                            │                                      │
    ├── 赛前：报名开放 ──────────┼── 进行中：组队、提交、投票 ──────────┤
    │                            │                                      ▼
    │                            │                                   收口（archived_at）
    │                            │                             · 作品进前序作品
    │                            │                             · 报名、投票开关关闭
    │                            │                             · 身份回落为普通用户
```

- **保留期**：`APP_SEASON_CLOSE_AFTER_DAYS`，默认 30。留给闭幕、结果公示、后续沟通。
- 保留期内这一届仍在进行中的页面上（画廊、实时动态），只是不能再报名、不能再投票。
  收口之后这些作品只出现在 `/exhibition/archive` 里。
- **作品在赛后默认继续可编辑**：`ends_at` 之后到收口之后都能提交新版本，只是不参与评比。
  见第 5 节。

---

## 2 谁决定「当期赛事」

`APP_CURRENT_EVENT_SLUG`，默认 `1024-challenge`。

这是**运维配置，不是数据**。理由：全站有三十多处按当期赛事过滤的查询，把「当前赛事」
做成数据库字段会引入一致的缓存与切换时序问题；换一届的代价就是改一个环境变量并重启。

换届步骤：

1. 后台 `POST /api/v1/admin/events` 建下一届（默认 `draft`，报名与投票都关着）
2. 排赛程：`PATCH /api/v1/admin/event/schedule`
3. 改 `APP_CURRENT_EVENT_SLUG`，重启 `go-api`
4. 开关报名：`PUT /api/v1/admin/event/switches` `{"registration_open": true}`

往届数据不受影响：`companies`、`projects`、`memberships` 都带 `event_id`。

---

## 3 报名

按届记录在 `event_enrollments`（`event_id + user_id` 唯一）：

| 身份 | 含义 | 对 `users.role` 的影响 |
| --- | --- | --- |
| `participant` | 参赛者，之后要组队 | `participant → member` |
| `audience` | 观众，只看和投票 | `member → participant`（在公司里则拒绝） |

规则：

- 只有 `registration_open = true`、未结束、未收口的赛事能报名。
- 比赛开始前可以改选身份，也可以取消报名（`status` 改成 `withdrawn`，记录不删）。
- 比赛开始后不能取消报名，退赛由管理员处理。
- **报名不改动 `staff` / `admin`**：那是平台常驻角色，不随赛季变化。

`POST /api/v1/me/participation`（旧的旁观/参赛切换）保留契约兼容，内部走同一条写入路径，
因此同样受报名开关约束 —— 否则空档期的身份回落会被这个接口绕过。

---

## 4 收口（赛季关闭）

后台任务 `seasonLoop` 每 `APP_SEASON_POLL_SECONDS`（默认 3600 秒）扫一次，同时做两件事：

### 4.1 归档到期赛事

```sql
UPDATE events
SET stage = 'archived', archived_at = now(),
    registration_open = false, voting_open = false, updated_at = now()
WHERE archived_at IS NULL
  AND ends_at IS NOT NULL
  AND ends_at <= now() - 保留期
RETURNING id, slug;
```

单条语句认领，多实例同时跑也只会成功一次。

**为什么用 `archived_at` 而不是 `stage`**：`stage` 是工作人员手工排期用的展示字段
（`draft / registration / build / judging / results`），系统判断不能依赖人工输入。
一个人把 stage 打成 `archived` 不该让作品凭空进入往届。

### 4.2 空档期身份回落

当期赛事已经收口（`archived_at` 非空，或结束时间加保留期已过）时：

```sql
UPDATE users SET role = 'participant'
WHERE role IN ('member', 'captain');
```

- 只降不升，管理员与工作人员不在集合里，永远不会被降。
- 自然幂等：降过之后下一次扫描匹配不到行，也不会再写审计。
- 未排期的赛事（`ends_at` 为空）一律不算空档期 —— 刚建好还没排期的新赛事不该把所有人降级。

### 4.3 收口后的写入

- 作品编辑（保存、发布、回滚）：**默认仍然允许**，见第 5 节。
- 投票：`voting_open` 已在收口时置 false，且 `votingEvent` 把已收口的赛事直接当作关闭。
- 建队、加入、邀请：受 `registration_open` 约束。

---

## 5 赛后继续提交作品

比赛（投票与路演）结束后，团队往往还要继续打磨作品。默认行为是**赛后仍然可以提交
新版本**，包括赛季收口之后；只是这些版本不参与评比。

开关是赛事级的 `events.post_event_edits`（默认 `true`）：

| 取值 | 行为 |
| --- | --- |
| `true`（默认） | `ends_at` 之后、以及收口之后，队长与提交人都能保存草稿、发布新版本、回滚 |
| `false` | `ends_at` 一过作品即只读，保存/发布/回滚/编辑锁一律 409 |

后台改：`PUT /api/v1/admin/event/switches` `{"post_event_edits": false}`。

判定只有一个纯函数（`editLocked`），保存、发布、回滚、编辑锁全部共用：

```
post_event_edits = true            → 永不止读
post_event_edits = false           → archived_at 非空，或 now >= ends_at → 只读
```

**为什么放在赛事上**：不同届的诉求不一样——当届要开放打磨，往届可能需要冻结。
放在全局配置里就只能在代码里改。

### 5.1 参赛版本与赛后版本

作品版本是只增不改的（`project_versions`），所以「评委看到的那一版」永远查得到：

- **参赛版本** = `published_at <= ends_at` 的最大版本号
- **线上版本** = `projects.live_version`

`GET /exhibition/archive/{event_id}` 两样都返回。两者不同就说明赛后还在更新，
前端可以标一句「赛后更新」。这里**不新增字段、不做快照**——版本历史本身就是快照，
用「截至结束时间已发布的最大版本」算即可，不存在「归档任务什么时候跑」导致的取错版本问题。

### 5.2 前序作品展示什么

展示**最新内容**（`projects` 当前行），并在作品上带 `competition_version` 标注。
也就是：作品继续更新，前序作品跟着更新，但「当时评的是哪一版」始终可查。

---

## 6 前序作品

| 接口 | 内容 |
| --- | --- |
| `GET /api/v1/exhibition/archive` | 所有已收口的赛事，含公司数、作品数 |
| `GET /api/v1/exhibition/archive/{event_id}` | 某一届的全部已发布作品与获奖 |

只认 `archived_at`。未收口的赛事返回 404，作品仍在进行中的页面上。

同时，`GET /api/v1/projects` 与 `GET /api/v1/exhibition/feed` 改为**只覆盖当期赛事**，
收口后返回空列表 —— 往届作品不会同时出现在两个地方。

---

## 7 多赛季的数据正确性

赛季一多，原来「全库只有一届」的假设会出错。已修的地方：

| 位置 | 原来的问题 |
| --- | --- |
| `addMember` / 邀请查重 | 只按 `user_id` 查成员关系，上一届的成员会被挡住，进不了新一届 |
| `myCompanyID` | 同上，广告、介绍任务会指向上一届的公司 |
| `myProject` | 返回上一届的作品 |
| `usageTeam` | 团队用量统计到上一届的公司 |
| 广播受众 `members` | 把历届所有当过成员的人都算进来 |

统一按 `companies.event_id` 限定：查「我的公司」用当期赛事，查「是否已在其他公司」
用目标公司所属的那一届。

---

## 8 配置项

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `APP_CURRENT_EVENT_SLUG` | `1024-challenge` | 当期赛事，换届时改 |
| `APP_SEASON_CLOSE_AFTER_DAYS` | `30` | 结束满多少天正式收口；0 = 结束即收口 |
| `APP_SEASON_POLL_SECONDS` | `3600` | 赛季任务轮询间隔；0 = 关闭自动收口。设有 60 秒下限，调小不会更勤 |

关闭自动收口（`APP_SEASON_POLL_SECONDS=0`）时，收口需要人工写 `archived_at` 与开关；
**不建议**，因为身份回落也会一起停掉。

---

## 9 未做的事

| 项 | 说明 |
| --- | --- |
| 后台切换当期赛事 | 仍然是环境变量 + 重启；做成界面要动三十多处查询，单独排期 |
| 往届作品的评论 | 收口后评论接口没有关，但也没有入口；是否保留待定 |
| 跨届个人档案 | 某人历届参加过的队伍与作品，现在只能从 `/me/enrollments` 看到报名 |
| 归档通知 | 收口时给参赛者发一封邮件，还没做 |
