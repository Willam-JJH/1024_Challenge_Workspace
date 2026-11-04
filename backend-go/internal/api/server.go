package api

import (
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/jackc/pgx/v5/pgxpool"

	"challenge-api/internal/auth"
	"challenge-api/internal/config"
	"challenge-api/internal/db"
)

const APIVersion = "0.1.0"

type Server struct {
	cfg            config.Config
	pools          *db.Pools
	logger         *slog.Logger
	store          *auth.Store
	mailer         auth.Mailer
	trustedProxies []netip.Prefix
}

// New 组装路由：先注册已实现的 handler，其余 OpenAPI operation 一律 501，
// 保证契约先行的约定在 Go 侧同样成立。
func New(cfg config.Config, pools *db.Pools, logger *slog.Logger) (http.Handler, error) {
	mailer, err := auth.NewMailer(auth.MailOptions{
		Driver:   cfg.MailDriver,
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		TLS:      cfg.SMTPTLS,
		From:     cfg.MailFrom,
		FromName: cfg.MailFromName,
		Timeout:  cfg.SMTPTimeout,
	}, logger)
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:            cfg,
		pools:          pools,
		logger:         logger,
		mailer:         mailer,
		trustedProxies: parsePrefixes(cfg.TrustedProxies),
	}
	if pools != nil && pools.Write != nil {
		s.store = auth.NewStore(pools.Write)
	}

	mux := http.NewServeMux()
	implemented := s.implemented()
	for pattern, handler := range implemented {
		mux.HandleFunc(pattern, handler)
	}
	for _, pattern := range stubOps {
		if _, ok := implemented[pattern]; ok {
			continue
		}
		mux.HandleFunc(pattern, s.notImplemented)
	}

	var handler http.Handler = mux
	handler = withErrorEnvelope(handler)
	handler = withCORS(cfg.CORSOrigins, handler)
	handler = withBodyBuffer(1<<20, handler)
	handler = withAccessLog(logger, handler)
	handler = withRequestID(handler)
	handler = withRecover(logger, handler)
	return handler, nil
}

func (s *Server) implemented() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		healthPattern:                    s.health,
		loginPattern:                     s.login,
		logoutCurrentPattern:             s.logoutCurrent,
		logoutAllPattern:                 s.logoutAll,
		mePattern:                        s.me,
		eventsPattern:                    s.listEvents,
		eventCurrentPattern:              s.currentEvent,
		eventGetPattern:                  s.getEvent,
		eventStatsPattern:                s.eventStats,
		galleryPattern:                   s.gallery,
		feedPattern:                      s.feed,
		promotionsPattern:                s.listPromotions,
		commentsPattern:                  s.listComments,
		boardPattern:                     s.board,
		bigScreenPattern:                 s.bigScreen,
		boardStreamPattern:               s.streamBoard,
		pitchPattern:                     s.pitch,
		pitchSourcesPattern:              s.pitchSources,
		pitchMessagesPattern:             s.pitchMessages,
		companiesPattern:                 s.listCompanies,
		companyGetPattern:                s.getCompany,
		companyMembersPattern:            s.listMembers,
		adminDashboardPattern:            s.adminDashboard,
		adminUsersPattern:                s.adminUsers,
		adminAuditPattern:                s.adminAudit,
		adminAuditActionsPattern:         s.adminAuditActions,
		adminEventPattern:                s.adminEvent,
		changePasswordPattern:            s.changePassword,
		resetRequestPattern:              s.requestPasswordReset,
		resetGrantPattern:                s.grantPasswordReset,
		resetPattern:                     s.resetPassword,
		registerPattern:                  s.register,
		resendRegistrationPattern:        s.resendRegistration,
		verifyRegistrationPattern:        s.verifyRegistration,
		activationPreviewPattern:         s.previewActivation,
		activatePattern:                  s.activate,
		botChallengePattern:              s.botChallenge,
		sessionsPattern:                  s.listSessions,
		revokeSessionPattern:             s.revokeSession,
		myProjectPattern:                 s.myProject,
		draftSchemaPattern:               s.draftSchema,
		projectGetPattern:                s.getProject,
		editSessionPattern:               s.acquireEditSession,
		heartbeatPattern:                 s.heartbeatLock,
		releaseLockPattern:               s.releaseLock,
		saveDraftPattern:                 s.saveDraft,
		publishPattern:                   s.publishProject,
		versionsPattern:                  s.listVersions,
		versionPattern:                   s.getVersion,
		restoreVersionPattern:            s.restoreVersion,
		attendanceGetPattern:             s.getAttendance,
		attendancePutPattern:             s.setAttendance,
		agentApprovalPattern:             s.approveAgentDraft,
		createCommentPattern:             s.createComment,
		editCommentPattern:               s.editComment,
		deleteCommentPattern:             s.deleteComment,
		reportCommentPattern:             s.reportComment,
		createPromoPattern:               s.createPromotion,
		editPromoPattern:                 s.editPromotion,
		deletePromoPattern:               s.deletePromotion,
		passportPattern:                  s.myPassport,
		stampPattern:                     s.stamp,
		removeStampPattern:               s.removeStamp,
		companyStampsPattern:             s.companyStamps,
		adminCreateUserPattern:           s.adminCreateUser,
		adminUserGetPattern:              s.adminGetUser,
		adminUserTeamsPattern:            s.adminUserTeamsHandler,
		adminUserRolePattern:             s.adminSetRole,
		adminUserModePattern:             s.adminSetMode,
		adminUserLocalePattern:           s.adminSetLocale,
		adminUserSuspendPattern:          s.adminSuspend,
		adminUserResumePattern:           s.adminResume,
		adminUserSignOutPattern:          s.adminForceSignOut,
		adminUserResendPattern:           s.adminResendActivation,
		adminUserDeletePattern:           s.adminDeleteUser,
		adminBulkRolePattern:             s.adminBulkRole,
		adminBulkLocalePattern:           s.adminBulkLocale,
		adminBulkDeletePattern:           s.adminBulkDelete,
		adminExportCSVPattern:            s.adminExportCSV,
		adminVotingPattern:               s.adminVoting,
		adminBallotsPattern:              s.adminBallots,
		adminVoteEventsPattern:           s.adminVoteEvents,
		adminVisibilityPattern:           s.adminVisibility,
		adminVoidsPattern:                s.adminVoids,
		adminShortlistPattern:            s.adminAnnounceShortlist,
		adminShortlistDeletePattern:      s.adminWithdrawShortlist,
		adminAwardPattern:                s.adminCreateAward,
		adminAwardDeletePattern:          s.adminDeleteAward,
		adminCompaniesPattern:            s.adminCompanies,
		adminCompanyPattern:              s.adminGetCompany,
		adminDissolvePattern:             s.adminDissolve,
		adminRosterLockPattern:           s.adminRosterLock,
		adminCaptainPattern:              s.adminTransferCaptain,
		adminSubmitterPattern:            s.adminAppointSubmitter,
		adminMemberMovePattern:           s.adminMoveMember,
		adminProjectsPattern:             s.adminProjects,
		adminProjectPattern:              s.adminProject,
		adminProjectVersionsPattern:      s.adminProjectVersions,
		adminProjectVersionPattern:       s.adminProjectVersion,
		createCompanyPattern:             s.createCompany,
		updateCompanyPattern:             s.updateCompany,
		removeMemberPattern:              s.removeMember,
		leaveCompanyPattern:              s.leaveCompany,
		companyCaptainPattern:            s.companyTransferCaptain,
		companySubmitterPattern:          s.companyAppointSubmitter,
		companyRosterPattern:             s.companyRosterLock,
		invitationsPattern:               s.listInvitations,
		invitePattern:                    s.inviteMember,
		revokeInvitePattern:              s.revokeInvitation,
		redeliverInvitePattern:           s.redeliverInvitation,
		inviteLinkPattern:                s.createInviteLink,
		disableLinksPattern:              s.disableInviteLinks,
		joinRequestsPattern:              s.listJoinRequests,
		joinRequestPattern:               s.createJoinRequest,
		acceptJoinPattern:                s.acceptJoinRequest,
		declineJoinPattern:               s.declineJoinRequest,
		introductionsPattern:             s.listIntroductions,
		introTextPattern:                 s.setIntroductionText,
		introReplyPattern:                s.setIntroductionReply,
		introDeleteReplyPattern:          s.deleteIntroductionReply,
		collaborationsPattern:            s.listCollaborations,
		offlineSessionsPattern:           s.listOfflineSessions,
		offlineSessionPattern:            s.getOfflineSession,
		offlineCalendarPattern:           s.offlineCalendar,
		offlineRsvpPattern:               s.offlineRsvp,
		offlineCancelPattern:             s.cancelOfflineRsvp,
		announcementsPattern:             s.listAnnouncements,
		sponsorsPattern:                  s.listSponsors,
		cspReportPattern:                 s.cspReport,
		judgingProjectsPattern:           s.judgingProjects,
		judgingScorePattern:              s.saveScore,
		assetCreatePattern:               s.createAsset,
		assetContentPattern:              s.uploadAssetContent,
		assetPartPattern:                 s.uploadAssetPart,
		assetCompletePattern:             s.completeAsset,
		assetGetPattern:                  s.getAsset,
		assetPatchPattern:                s.patchAsset,
		assetDeletePattern:               s.deleteAsset,
		fileReadPattern:                  s.readAssetFile,
		fileVariantPattern:               s.readAssetVariant,
		wellKnownPattern:                 s.oauthMetadata,
		oauthAuthorizePattern:            s.oauthAuthorize,
		oauthConsentGetPattern:           s.oauthConsentInfo,
		oauthConsentPostPattern:          s.oauthConsent,
		oauthTokenPattern:                s.oauthTokenHandler,
		oauthRevokePattern:               s.oauthRevoke,
		oauthUserinfoPattern:             s.oauthUserinfo,
		connectedAppsPattern:             s.connectedApps,
		revokeAppPattern:                 s.revokeConnectedApp,
		revokeDevicePattern:              s.revokeConnectedDevice,
		usageSummaryPattern:              s.usageSummary,
		usageRequestsPattern:             s.usageRequests,
		usageTeamPattern:                 s.usageTeam,
		llmModelsPattern:                 s.listLLMModels,
		llmChatPattern:                   s.chatCompletions,
		llmEmbeddingsPattern:             s.llmNotImplemented,
		llmResponsesPattern:              s.llmNotImplemented,
		llmSearchPattern:                 s.llmNotImplemented,
		adminLLMOverviewPattern:          s.adminLLMOverview,
		adminLLMBudgetGetPattern:         s.adminLLMBudget,
		adminLLMBudgetPutPattern:         s.adminLLMSetBudget,
		adminLLMModelsPattern:            s.adminLLMModels,
		adminLLMModelCreatePattern:       s.adminLLMCreateModel,
		adminLLMModelPattern:             s.adminLLMPatchModel,
		adminLLMModelDeletePattern:       s.adminLLMDeleteModel,
		adminLLMUpstreamsPattern:         s.adminLLMUpstreams,
		adminLLMUpstreamCreatePattern:    s.adminLLMCreateUpstream,
		adminLLMUpstreamPattern:          s.adminLLMPatchUpstream,
		adminLLMUpstreamDeletePattern:    s.adminLLMDeleteUpstream,
		adminLLMRouteCreatePattern:       s.adminLLMCreateRoute,
		adminLLMRoutePattern:             s.adminLLMPatchRoute,
		adminLLMRouteDeletePattern:       s.adminLLMDeleteRoute,
		adminLLMKillSwitchesPattern:      s.adminLLMKillSwitches,
		adminLLMKillGlobalPattern:        s.adminLLMSetGlobalSwitch,
		adminLLMKillModelPattern:         s.adminLLMSetModelSwitch,
		adminLLMKillUserPattern:          s.adminLLMSetUserSwitch,
		adminLLMUsageTeamsPattern:        s.adminLLMUsageTeams,
		adminLLMUsageUsersPattern:        s.adminLLMUsageUsers,
		mePatchPattern:                   s.updateMe,
		apiKeysPattern:                   s.listAPIKeys,
		apiKeyCreatePattern:              s.createAPIKey,
		apiKeyRevokePattern:              s.revokeAPIKey,
		apiKeyRotatePattern:              s.rotateAPIKey,
		myVotesPattern:                   s.getMyVotes,
		myVotesPutPattern:                s.replaceMyVotes,
		myVotePattern:                    s.castVote,
		myVoteDeletePattern:              s.withdrawVote,
		membershipPattern:                s.myMembershipHandler,
		participationPattern:             s.setParticipation,
		myInvitationsPattern:             s.myInvitations,
		myInvitationAcceptPattern:        s.acceptInvitation,
		myInvitationDeclinePattern:       s.declineInvitation,
		myJoinRequestsPattern:            s.myJoinRequests,
		myJoinRequestPattern:             s.withdrawJoinRequest,
		notificationsPattern:             s.listNotifications,
		notificationsReadPattern:         s.markNotificationsRead,
		digestPattern:                    s.getDigestPreferences,
		digestPutPattern:                 s.setDigestPreferences,
		visitsPattern:                    s.myVisits,
		deletionRequestPattern:           s.requestDeletion,
		deletionCancelPattern:            s.cancelDeletion,
		exhibitionAssignmentPattern:      s.exhibitionAssignment,
		finderCompaniesPattern:           s.finderCompanies,
		finderPeoplePattern:              s.finderPeople,
		invitationPreviewPattern:         s.previewInvitation,
		invitationAcceptPattern:          s.acceptInvitation,
		invitationDeclinePattern:         s.declineInvitation,
		inviteLinkAcceptPattern:          s.acceptInviteLink,
		resultsPattern:                   s.results,
		shortlistPattern:                 s.shortlist,
		adminSearchPattern:               s.adminSearch,
		linkPreviewPattern:               s.linkPreview,
		githubRepoPattern:                s.githubRepo,
		adminSchedulePattern:             s.adminSetSchedule,
		adminSwitchesPattern:             s.adminSetSwitches,
		adminMediaDeletePattern:          s.adminRemoveAsset,
		adminFlagsPattern:                s.adminListFlags,
		adminFlagCreatePattern:           s.adminCreateFlag,
		adminFlagPattern:                 s.adminPatchFlag,
		adminFlagDeletePattern:           s.adminDeleteFlag,
		adminAnnouncementsPattern:        s.adminListAnnouncements,
		adminAnnouncementCreatePattern:   s.adminCreateAnnouncement,
		adminAnnouncementPattern:         s.adminPatchAnnouncement,
		adminAnnouncementDeletePattern:   s.adminDeleteAnnouncement,
		adminSponsorsPattern:             s.adminListSponsors,
		adminSponsorCreatePattern:        s.adminCreateSponsor,
		adminSponsorPattern:              s.adminPatchSponsor,
		adminSponsorDeletePattern:        s.adminDeleteSponsor,
		adminSponsorOrderPattern:         s.adminOrderSponsors,
		adminOfflineSessionsPattern:      s.adminOfflineSessions,
		adminOfflineCreatePattern:        s.adminCreateOfflineSession,
		adminOfflineSessionPattern:       s.adminPatchOfflineSession,
		adminOfflineDeletePattern:        s.adminDeleteOfflineSession,
		adminOfflineCancelPattern:        s.adminCancelOfflineSession,
		adminOfflineRsvpsPattern:         s.adminOfflineRsvps,
		adminOfflineCheckInPattern:       s.adminCheckIn,
		adminPitchPatchPattern:           s.adminPatchPitch,
		adminPitchSourcesPattern:         s.adminPitchSources,
		adminPitchSourceCreate:           s.adminCreatePitchSource,
		adminPitchSourcePattern:          s.adminPatchPitchSource,
		adminPitchSourceDelete:           s.adminDeletePitchSource,
		adminPitchSourceMove:             s.adminMovePitchSource,
		adminPitchMessagesPattern:        s.adminPitchMessages,
		adminPitchModeratePattern:        s.adminModeratePitchMessage,
		pitchMessageCreate:               s.createPitchMessage,
		pitchMessageDelete:               s.deletePitchMessage,
		pitchStreamPattern:               s.pitchStream,
		adminEmailBroadcastsPattern:      s.adminEmailBroadcasts,
		adminEmailBroadcastCreate:        s.adminCreateBroadcast,
		adminEmailBroadcastPattern:       s.adminPatchBroadcast,
		adminEmailBroadcastDelete:        s.adminDeleteBroadcast,
		adminEmailCancelPattern:          s.adminCancelBroadcast,
		adminEmailSchedulePattern:        s.adminEmailSchedule,
		adminEmailTestPattern:            s.adminEmailTestBroadcast,
		adminEmailPreviewPattern:         s.adminEmailPreview,
		adminEmailEstimatePattern:        s.adminEmailEstimate,
		adminEmailMessagesPattern:        s.adminEmailMessages,
		adminEmailResendPattern:          s.adminEmailResend,
		adminEmailTemplatesPattern:       s.adminEmailTemplates,
		adminEmailTemplatePreview:        s.adminEmailTemplatePreview,
		adminEmailSetupPattern:           s.adminEmailSetup,
		adminEmailQuotaPattern:           s.adminEmailQuota,
		adminExhibitionPattern:           s.adminExhibitionOverview,
		adminAssignmentsPattern:          s.adminAssignments,
		adminAssignmentPattern:           s.adminPatchAssignment,
		adminAssignmentCancelPattern:     s.adminCancelAssignment,
		adminAssignmentGivenPattern:      s.adminMarkGiven,
		adminAssignmentReassignPattern:   s.adminReassign,
		adminAssignmentsRemainingPattern: s.adminAssignRemaining,
		adminAssignmentsGeneratePattern:  s.adminGenerateAssignments,
		adminAssignmentsPublishPattern:   s.adminPublishAssignments,
		adminAssignmentsResetPattern:     s.adminResetAssignments,
		adminAssignmentsRevealPattern:    s.adminRevealAll,
		adminExhibitionCollabPattern:     s.adminExhibitionCollaborations,
		adminExhibitionCommentsPattern:   s.adminExhibitionComments,
		adminIntroModeratePattern:        s.adminModerateIntroduction,
		adminReplyModeratePattern:        s.adminModerateReply,
		adminPresencePattern:             s.adminSetPresence,
		adminExhibitionPromotionsPattern: s.adminExhibitionPromotions,
	}
}

// readPool 返回只读连接池，将来接只读副本时只改配置。
func (s *Server) readPool() *pgxpool.Pool {
	if s.pools == nil {
		return nil
	}
	return s.pools.Read
}

func (s *Server) writePool() *pgxpool.Pool {
	if s.pools == nil {
		return nil
	}
	return s.pools.Write
}

func (s *Server) notImplemented(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, ErrNotImplemented)
}
