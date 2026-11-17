CREATE TABLE alembic_version (
    version_num VARCHAR(32) NOT NULL, 
    CONSTRAINT alembic_version_pkc PRIMARY KEY (version_num)
);

-- Running upgrade  -> eae3777bb79c

CREATE TABLE events (
    slug VARCHAR(80) NOT NULL, 
    name VARCHAR(160) NOT NULL, 
    stage VARCHAR(32) NOT NULL, 
    starts_at TIMESTAMP WITHOUT TIME ZONE, 
    ends_at TIMESTAMP WITHOUT TIME ZONE, 
    registration_open BOOLEAN NOT NULL, 
    voting_open BOOLEAN NOT NULL, 
    votes_visible BOOLEAN NOT NULL, 
    llm_budget_micro INTEGER NOT NULL, 
    llm_kill_switch BOOLEAN NOT NULL, 
    pitch_title VARCHAR(160) NOT NULL, 
    pitch_on_air BOOLEAN NOT NULL, 
    company_seats INTEGER NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ix_events_slug ON events (slug);

CREATE TABLE llm_models (
    name VARCHAR(120) NOT NULL, 
    input_price_micro INTEGER NOT NULL, 
    output_price_micro INTEGER NOT NULL, 
    context_window INTEGER NOT NULL, 
    enabled BOOLEAN NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ix_llm_models_name ON llm_models (name);

CREATE TABLE llm_upstreams (
    name VARCHAR(120) NOT NULL, 
    base_url TEXT NOT NULL, 
    api_key TEXT NOT NULL, 
    enabled BOOLEAN NOT NULL, 
    priority INTEGER NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id)
);

CREATE TABLE oauth_clients (
    name VARCHAR(120) NOT NULL, 
    client_id VARCHAR(64) NOT NULL, 
    redirect_uris TEXT NOT NULL, 
    scopes VARCHAR(200) NOT NULL, 
    is_public BOOLEAN NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ix_oauth_clients_client_id ON oauth_clients (client_id);

CREATE TABLE users (
    email VARCHAR(320) NOT NULL, 
    password_hash VARCHAR(255) NOT NULL, 
    display_name VARCHAR(120) NOT NULL, 
    locale VARCHAR(8) NOT NULL, 
    role VARCHAR(32) NOT NULL, 
    status VARCHAR(32) NOT NULL, 
    mode VARCHAR(16) NOT NULL, 
    llm_blocked BOOLEAN NOT NULL, 
    digest_comments BOOLEAN NOT NULL, 
    deletion_requested_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id)
);

CREATE UNIQUE INDEX ix_users_email ON users (email);

CREATE TABLE verification_codes (
    email VARCHAR(320) NOT NULL, 
    purpose VARCHAR(32) NOT NULL, 
    code_hash VARCHAR(64) NOT NULL, 
    expires_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    consumed_at TIMESTAMP WITHOUT TIME ZONE, 
    attempts INTEGER NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id)
);

CREATE INDEX ix_verification_codes_code_hash ON verification_codes (code_hash);

CREATE INDEX ix_verification_codes_email ON verification_codes (email);

CREATE INDEX ix_verification_codes_purpose ON verification_codes (purpose);

CREATE TABLE announcements (
    event_id VARCHAR(32) NOT NULL, 
    title VARCHAR(160) NOT NULL, 
    body TEXT NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    published_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE
);

CREATE INDEX ix_announcements_event_id ON announcements (event_id);

CREATE TABLE api_keys (
    user_id VARCHAR(32) NOT NULL, 
    label VARCHAR(80) NOT NULL, 
    prefix VARCHAR(16) NOT NULL, 
    key_hash VARCHAR(64) NOT NULL, 
    last_used_at TIMESTAMP WITHOUT TIME ZONE, 
    revoked_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE, 
    CONSTRAINT uq_api_keys_key_hash UNIQUE (key_hash)
);

CREATE INDEX ix_api_keys_user_id ON api_keys (user_id);

CREATE TABLE audit_log (
    actor_id VARCHAR(32), 
    action VARCHAR(80) NOT NULL, 
    target_type VARCHAR(60) NOT NULL, 
    target_id VARCHAR(64), 
    note TEXT, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(actor_id) REFERENCES users (id) ON DELETE SET NULL
);

CREATE INDEX ix_audit_log_action ON audit_log (action);

CREATE INDEX ix_audit_log_actor_id ON audit_log (actor_id);

CREATE TABLE broadcasts (
    event_id VARCHAR(32) NOT NULL, 
    title VARCHAR(160) NOT NULL, 
    body TEXT NOT NULL, 
    audience VARCHAR(32) NOT NULL, 
    locale VARCHAR(8) NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    scheduled_at TIMESTAMP WITHOUT TIME ZONE, 
    sent_at TIMESTAMP WITHOUT TIME ZONE, 
    created_by VARCHAR(32), 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(created_by) REFERENCES users (id) ON DELETE SET NULL, 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE
);

CREATE INDEX ix_broadcasts_event_id ON broadcasts (event_id);

CREATE TABLE companies (
    event_id VARCHAR(32) NOT NULL, 
    slug VARCHAR(80) NOT NULL, 
    name VARCHAR(120) NOT NULL, 
    blurb TEXT, 
    captain_id VARCHAR(32), 
    roster_locked BOOLEAN NOT NULL, 
    present BOOLEAN NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(captain_id) REFERENCES users (id) ON DELETE SET NULL, 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    CONSTRAINT uq_companies_event_slug UNIQUE (event_id, slug)
);

CREATE INDEX ix_companies_event_id ON companies (event_id);

CREATE TABLE feature_flags (
    event_id VARCHAR(32) NOT NULL, 
    key VARCHAR(80) NOT NULL, 
    description TEXT, 
    enabled BOOLEAN NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    CONSTRAINT uq_feature_flag UNIQUE (event_id, key)
);

CREATE INDEX ix_feature_flags_event_id ON feature_flags (event_id);

CREATE TABLE llm_routes (
    model_id VARCHAR(32) NOT NULL, 
    upstream_id VARCHAR(32) NOT NULL, 
    upstream_model VARCHAR(160) NOT NULL, 
    priority INTEGER NOT NULL, 
    enabled BOOLEAN NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(model_id) REFERENCES llm_models (id) ON DELETE CASCADE, 
    FOREIGN KEY(upstream_id) REFERENCES llm_upstreams (id) ON DELETE CASCADE, 
    CONSTRAINT uq_llm_route UNIQUE (model_id, upstream_id)
);

CREATE INDEX ix_llm_routes_model_id ON llm_routes (model_id);

CREATE INDEX ix_llm_routes_upstream_id ON llm_routes (upstream_id);

CREATE TABLE llm_usage (
    user_id VARCHAR(32) NOT NULL, 
    model_id VARCHAR(32), 
    prompt_tokens INTEGER NOT NULL, 
    completion_tokens INTEGER NOT NULL, 
    cost_micro INTEGER NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(model_id) REFERENCES llm_models (id) ON DELETE SET NULL, 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX ix_llm_usage_model_id ON llm_usage (model_id);

CREATE INDEX ix_llm_usage_user_id ON llm_usage (user_id);

CREATE TABLE notifications (
    user_id VARCHAR(32) NOT NULL, 
    kind VARCHAR(32) NOT NULL, 
    title VARCHAR(160) NOT NULL, 
    body TEXT, 
    read_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX ix_notifications_user_id ON notifications (user_id);

CREATE TABLE oauth_codes (
    client_id VARCHAR(64) NOT NULL, 
    user_id VARCHAR(32) NOT NULL, 
    redirect_uri VARCHAR(500) NOT NULL, 
    code_hash VARCHAR(64) NOT NULL, 
    code_challenge VARCHAR(128) NOT NULL, 
    scope VARCHAR(200) NOT NULL, 
    expires_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    consumed_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(client_id) REFERENCES oauth_clients (client_id) ON DELETE CASCADE, 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX ix_oauth_codes_client_id ON oauth_codes (client_id);

CREATE UNIQUE INDEX ix_oauth_codes_code_hash ON oauth_codes (code_hash);

CREATE INDEX ix_oauth_codes_user_id ON oauth_codes (user_id);

CREATE TABLE oauth_consents (
    user_id VARCHAR(32) NOT NULL, 
    client_id VARCHAR(64) NOT NULL, 
    scope VARCHAR(200) NOT NULL, 
    revoked_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(client_id) REFERENCES oauth_clients (client_id) ON DELETE CASCADE, 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE, 
    CONSTRAINT uq_consent UNIQUE (user_id, client_id)
);

CREATE INDEX ix_oauth_consents_client_id ON oauth_consents (client_id);

CREATE INDEX ix_oauth_consents_user_id ON oauth_consents (user_id);

CREATE TABLE oauth_tokens (
    client_id VARCHAR(64) NOT NULL, 
    user_id VARCHAR(32) NOT NULL, 
    device_id VARCHAR(64) NOT NULL, 
    access_hash VARCHAR(64) NOT NULL, 
    refresh_hash VARCHAR(64) NOT NULL, 
    scope VARCHAR(200) NOT NULL, 
    expires_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    revoked_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(client_id) REFERENCES oauth_clients (client_id) ON DELETE CASCADE, 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX ix_oauth_tokens_access_hash ON oauth_tokens (access_hash);

CREATE INDEX ix_oauth_tokens_client_id ON oauth_tokens (client_id);

CREATE INDEX ix_oauth_tokens_device_id ON oauth_tokens (device_id);

CREATE UNIQUE INDEX ix_oauth_tokens_refresh_hash ON oauth_tokens (refresh_hash);

CREATE INDEX ix_oauth_tokens_user_id ON oauth_tokens (user_id);

CREATE TABLE offline_sessions (
    event_id VARCHAR(32) NOT NULL, 
    title VARCHAR(160) NOT NULL, 
    description TEXT, 
    location VARCHAR(200), 
    starts_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    ends_at TIMESTAMP WITHOUT TIME ZONE, 
    seats INTEGER NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE
);

CREATE INDEX ix_offline_sessions_event_id ON offline_sessions (event_id);

CREATE TABLE pitch_messages (
    event_id VARCHAR(32) NOT NULL, 
    author_id VARCHAR(32) NOT NULL, 
    body VARCHAR(280) NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(author_id) REFERENCES users (id) ON DELETE CASCADE, 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE
);

CREATE INDEX ix_pitch_messages_author_id ON pitch_messages (author_id);

CREATE INDEX ix_pitch_messages_event_id ON pitch_messages (event_id);

CREATE TABLE pitch_sources (
    event_id VARCHAR(32) NOT NULL, 
    kind VARCHAR(16) NOT NULL, 
    name VARCHAR(80) NOT NULL, 
    url TEXT NOT NULL, 
    region VARCHAR(16) NOT NULL, 
    enabled BOOLEAN NOT NULL, 
    position INTEGER NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE
);

CREATE INDEX ix_pitch_sources_event_id ON pitch_sources (event_id);

CREATE TABLE sessions (
    user_id VARCHAR(32) NOT NULL, 
    token_hash VARCHAR(64) NOT NULL, 
    user_agent VARCHAR(400), 
    client_ip VARCHAR(64), 
    last_seen_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    revoked_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX ix_sessions_token_hash ON sessions (token_hash);

CREATE INDEX ix_sessions_user_id ON sessions (user_id);

CREATE TABLE sponsors (
    event_id VARCHAR(32) NOT NULL, 
    name VARCHAR(120) NOT NULL, 
    logo_url VARCHAR(500), 
    website VARCHAR(500), 
    position INTEGER NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE
);

CREATE INDEX ix_sponsors_event_id ON sponsors (event_id);

CREATE TABLE collaborations (
    event_id VARCHAR(32) NOT NULL, 
    from_company_id VARCHAR(32) NOT NULL, 
    to_company_id VARCHAR(32) NOT NULL, 
    kind VARCHAR(16) NOT NULL, 
    body TEXT NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    FOREIGN KEY(from_company_id) REFERENCES companies (id) ON DELETE CASCADE, 
    FOREIGN KEY(to_company_id) REFERENCES companies (id) ON DELETE CASCADE
);

CREATE INDEX ix_collaborations_event_id ON collaborations (event_id);

CREATE INDEX ix_collaborations_from_company_id ON collaborations (from_company_id);

CREATE INDEX ix_collaborations_to_company_id ON collaborations (to_company_id);

CREATE TABLE email_messages (
    broadcast_id VARCHAR(32), 
    to_email VARCHAR(320) NOT NULL, 
    subject VARCHAR(200) NOT NULL, 
    body TEXT NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    error TEXT, 
    sent_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(broadcast_id) REFERENCES broadcasts (id) ON DELETE SET NULL
);

CREATE INDEX ix_email_messages_broadcast_id ON email_messages (broadcast_id);

CREATE INDEX ix_email_messages_to_email ON email_messages (to_email);

CREATE TABLE introductions (
    event_id VARCHAR(32) NOT NULL, 
    introducer_company_id VARCHAR(32) NOT NULL, 
    target_company_id VARCHAR(32) NOT NULL, 
    text TEXT, 
    reply TEXT, 
    reply_status VARCHAR(16) NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    given_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    FOREIGN KEY(introducer_company_id) REFERENCES companies (id) ON DELETE CASCADE, 
    FOREIGN KEY(target_company_id) REFERENCES companies (id) ON DELETE CASCADE
);

CREATE INDEX ix_introductions_event_id ON introductions (event_id);

CREATE INDEX ix_introductions_introducer_company_id ON introductions (introducer_company_id);

CREATE INDEX ix_introductions_target_company_id ON introductions (target_company_id);

CREATE TABLE invitations (
    company_id VARCHAR(32) NOT NULL, 
    email VARCHAR(320) NOT NULL, 
    token_hash VARCHAR(64) NOT NULL, 
    invited_by VARCHAR(32), 
    status VARCHAR(16) NOT NULL, 
    expires_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(company_id) REFERENCES companies (id) ON DELETE CASCADE, 
    FOREIGN KEY(invited_by) REFERENCES users (id) ON DELETE SET NULL, 
    CONSTRAINT uq_invitations_token UNIQUE (token_hash)
);

CREATE INDEX ix_invitations_company_id ON invitations (company_id);

CREATE INDEX ix_invitations_email ON invitations (email);

CREATE TABLE invite_links (
    company_id VARCHAR(32) NOT NULL, 
    token_hash VARCHAR(64) NOT NULL, 
    created_by VARCHAR(32), 
    disabled_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(company_id) REFERENCES companies (id) ON DELETE CASCADE, 
    FOREIGN KEY(created_by) REFERENCES users (id) ON DELETE SET NULL
);

CREATE INDEX ix_invite_links_company_id ON invite_links (company_id);

CREATE INDEX ix_invite_links_token_hash ON invite_links (token_hash);

CREATE TABLE join_requests (
    company_id VARCHAR(32) NOT NULL, 
    user_id VARCHAR(32) NOT NULL, 
    message TEXT, 
    status VARCHAR(16) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(company_id) REFERENCES companies (id) ON DELETE CASCADE, 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE, 
    CONSTRAINT uq_join_request UNIQUE (company_id, user_id)
);

CREATE INDEX ix_join_requests_company_id ON join_requests (company_id);

CREATE INDEX ix_join_requests_user_id ON join_requests (user_id);

CREATE TABLE media_assets (
    owner_id VARCHAR(32) NOT NULL, 
    company_id VARCHAR(32), 
    filename VARCHAR(200) NOT NULL, 
    content_type VARCHAR(120) NOT NULL, 
    size INTEGER NOT NULL, 
    purpose VARCHAR(32) NOT NULL, 
    alt VARCHAR(300), 
    status VARCHAR(16) NOT NULL, 
    storage_key VARCHAR(300) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(company_id) REFERENCES companies (id) ON DELETE SET NULL, 
    FOREIGN KEY(owner_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX ix_media_assets_owner_id ON media_assets (owner_id);

CREATE TABLE memberships (
    company_id VARCHAR(32) NOT NULL, 
    user_id VARCHAR(32) NOT NULL, 
    role VARCHAR(32) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(company_id) REFERENCES companies (id) ON DELETE CASCADE, 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE, 
    CONSTRAINT uq_membership UNIQUE (company_id, user_id)
);

CREATE INDEX ix_memberships_company_id ON memberships (company_id);

CREATE INDEX ix_memberships_user_id ON memberships (user_id);

CREATE TABLE offline_rsvps (
    session_id VARCHAR(32) NOT NULL, 
    user_id VARCHAR(32) NOT NULL, 
    answer VARCHAR(8) NOT NULL, 
    waitlisted BOOLEAN NOT NULL, 
    checked_in_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(session_id) REFERENCES offline_sessions (id) ON DELETE CASCADE, 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE, 
    CONSTRAINT uq_offline_rsvp UNIQUE (session_id, user_id)
);

CREATE INDEX ix_offline_rsvps_session_id ON offline_rsvps (session_id);

CREATE INDEX ix_offline_rsvps_user_id ON offline_rsvps (user_id);

CREATE TABLE projects (
    event_id VARCHAR(32) NOT NULL, 
    company_id VARCHAR(32) NOT NULL, 
    status VARCHAR(32) NOT NULL, 
    title VARCHAR(160) NOT NULL, 
    summary TEXT NOT NULL, 
    content TEXT NOT NULL, 
    revision INTEGER NOT NULL, 
    live_version INTEGER, 
    attendance VARCHAR(16), 
    agent_reviewed BOOLEAN NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(company_id) REFERENCES companies (id) ON DELETE CASCADE, 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    CONSTRAINT uq_projects_company UNIQUE (company_id)
);

CREATE INDEX ix_projects_company_id ON projects (company_id);

CREATE INDEX ix_projects_event_id ON projects (event_id);

CREATE TABLE promotions (
    company_id VARCHAR(32) NOT NULL, 
    kind VARCHAR(16) NOT NULL, 
    body TEXT NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    expires_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(company_id) REFERENCES companies (id) ON DELETE CASCADE
);

CREATE INDEX ix_promotions_company_id ON promotions (company_id);

CREATE TABLE votes (
    event_id VARCHAR(32) NOT NULL, 
    voter_id VARCHAR(32) NOT NULL, 
    company_id VARCHAR(32) NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(company_id) REFERENCES companies (id) ON DELETE CASCADE, 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    FOREIGN KEY(voter_id) REFERENCES users (id) ON DELETE CASCADE, 
    CONSTRAINT uq_vote UNIQUE (event_id, voter_id, company_id)
);

CREATE INDEX ix_votes_company_id ON votes (company_id);

CREATE INDEX ix_votes_event_id ON votes (event_id);

CREATE INDEX ix_votes_voter_id ON votes (voter_id);

CREATE TABLE awards (
    event_id VARCHAR(32) NOT NULL, 
    kind VARCHAR(32) NOT NULL, 
    title VARCHAR(120) NOT NULL, 
    project_id VARCHAR(32) NOT NULL, 
    announced_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    FOREIGN KEY(project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE INDEX ix_awards_event_id ON awards (event_id);

CREATE INDEX ix_awards_project_id ON awards (project_id);

CREATE TABLE comments (
    project_id VARCHAR(32) NOT NULL, 
    author_id VARCHAR(32) NOT NULL, 
    parent_id VARCHAR(32), 
    body TEXT NOT NULL, 
    status VARCHAR(16) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(author_id) REFERENCES users (id) ON DELETE CASCADE, 
    FOREIGN KEY(parent_id) REFERENCES comments (id) ON DELETE CASCADE, 
    FOREIGN KEY(project_id) REFERENCES projects (id) ON DELETE CASCADE
);

CREATE INDEX ix_comments_author_id ON comments (author_id);

CREATE INDEX ix_comments_parent_id ON comments (parent_id);

CREATE INDEX ix_comments_project_id ON comments (project_id);

CREATE TABLE project_edit_sessions (
    project_id VARCHAR(32) NOT NULL, 
    user_id VARCHAR(32) NOT NULL, 
    token_hash VARCHAR(64) NOT NULL, 
    expires_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(project_id) REFERENCES projects (id) ON DELETE CASCADE, 
    FOREIGN KEY(user_id) REFERENCES users (id) ON DELETE CASCADE
);

CREATE INDEX ix_project_edit_sessions_project_id ON project_edit_sessions (project_id);

CREATE INDEX ix_project_edit_sessions_token_hash ON project_edit_sessions (token_hash);

CREATE INDEX ix_project_edit_sessions_user_id ON project_edit_sessions (user_id);

CREATE TABLE project_versions (
    project_id VARCHAR(32) NOT NULL, 
    version INTEGER NOT NULL, 
    title VARCHAR(160) NOT NULL, 
    summary TEXT NOT NULL, 
    content TEXT NOT NULL, 
    published_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    published_by VARCHAR(32), 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(project_id) REFERENCES projects (id) ON DELETE CASCADE, 
    FOREIGN KEY(published_by) REFERENCES users (id) ON DELETE SET NULL, 
    CONSTRAINT uq_project_version UNIQUE (project_id, version)
);

CREATE INDEX ix_project_versions_project_id ON project_versions (project_id);

CREATE TABLE scores (
    project_id VARCHAR(32) NOT NULL, 
    judge_id VARCHAR(32) NOT NULL, 
    creativity INTEGER NOT NULL, 
    engineering INTEGER NOT NULL, 
    presentation INTEGER NOT NULL, 
    notes TEXT, 
    submitted_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(judge_id) REFERENCES users (id) ON DELETE CASCADE, 
    FOREIGN KEY(project_id) REFERENCES projects (id) ON DELETE CASCADE, 
    CONSTRAINT uq_score UNIQUE (project_id, judge_id)
);

CREATE INDEX ix_scores_judge_id ON scores (judge_id);

CREATE INDEX ix_scores_project_id ON scores (project_id);

CREATE TABLE shortlist_entries (
    event_id VARCHAR(32) NOT NULL, 
    project_id VARCHAR(32) NOT NULL, 
    position INTEGER NOT NULL, 
    announced_at TIMESTAMP WITHOUT TIME ZONE, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    FOREIGN KEY(project_id) REFERENCES projects (id) ON DELETE CASCADE, 
    CONSTRAINT uq_shortlist UNIQUE (event_id, project_id)
);

CREATE INDEX ix_shortlist_entries_event_id ON shortlist_entries (event_id);

CREATE INDEX ix_shortlist_entries_project_id ON shortlist_entries (project_id);

CREATE TABLE visits (
    event_id VARCHAR(32) NOT NULL, 
    project_id VARCHAR(32) NOT NULL, 
    visitor_id VARCHAR(32) NOT NULL, 
    note TEXT, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(event_id) REFERENCES events (id) ON DELETE CASCADE, 
    FOREIGN KEY(project_id) REFERENCES projects (id) ON DELETE CASCADE, 
    FOREIGN KEY(visitor_id) REFERENCES users (id) ON DELETE CASCADE, 
    CONSTRAINT uq_visit UNIQUE (event_id, project_id, visitor_id)
);

CREATE INDEX ix_visits_event_id ON visits (event_id);

CREATE INDEX ix_visits_project_id ON visits (project_id);

CREATE INDEX ix_visits_visitor_id ON visits (visitor_id);

CREATE TABLE comment_reports (
    comment_id VARCHAR(32) NOT NULL, 
    reporter_id VARCHAR(32) NOT NULL, 
    id VARCHAR(32) NOT NULL, 
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL, 
    PRIMARY KEY (id), 
    FOREIGN KEY(comment_id) REFERENCES comments (id) ON DELETE CASCADE, 
    FOREIGN KEY(reporter_id) REFERENCES users (id) ON DELETE CASCADE, 
    CONSTRAINT uq_comment_report UNIQUE (comment_id, reporter_id)
);

CREATE INDEX ix_comment_reports_comment_id ON comment_reports (comment_id);

CREATE INDEX ix_comment_reports_reporter_id ON comment_reports (reporter_id);

INSERT INTO alembic_version (version_num) VALUES ('eae3777bb79c') RETURNING alembic_version.version_num;
