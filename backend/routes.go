package main

import "net/http"

// registerRoutes is the auditable route table: the way this codebase's
// authentication posture is reviewed is by reading which routes go through
// withAuth and which do not. It lives apart from main.go so tests can build the
// exact production handler chain from it (see routes_test.go).
func registerRoutes(mux *http.ServeMux) {
	// Register frontend static file handler (no-op in headless mode)
	registerFrontend(mux)

	// Public Routes
	mux.HandleFunc("GET /api/v1/health/live", handleLiveness)
	mux.HandleFunc("GET /api/v1/health/ready", handleReadiness)
	mux.HandleFunc("GET /api/v1/health", handleReadiness)
	mux.HandleFunc("POST /api/v1/auth/bootstrap", handleBootstrap)
	mux.HandleFunc("POST /api/v1/auth/login", handleLogin)
	mux.HandleFunc("POST /api/v1/auth/invites/accept", handleAcceptInvite)
	// Only the refresh exchange is public: it authenticates by presenting a
	// valid refresh token in the body. Everything else here needs an existing
	// credential, so it goes through withAuth like every other guarded route —
	// the route table is how this codebase's auth posture is audited, and a bare
	// HandleFunc on a device route reads as public at a glance.
	mux.HandleFunc("POST /api/v1/auth/devices/refresh", handleRefreshDevice)

	// Authenticated Routes (Requires Session check)
	mux.Handle("POST /api/v1/auth/logout", withAuth(http.HandlerFunc(handleLogout), ""))
	mux.Handle("GET /api/v1/auth/me", withAuth(http.HandlerFunc(handleMe), ""))
	mux.Handle("POST /api/v1/auth/invites", withAuth(http.HandlerFunc(handleCreateInvite), "manage_community"))

	// Settings — profile & preferences
	mux.Handle("PATCH /api/v1/users/me", withAuth(http.HandlerFunc(handleUpdateProfile), ""))
	mux.Handle("GET /api/v1/users/me/preferences", withAuth(http.HandlerFunc(handleGetPreferences), ""))
	mux.Handle("PUT /api/v1/users/me/preferences", withAuth(http.HandlerFunc(handlePutPreferences), ""))

	// Settings — password & sessions & devices
	mux.Handle("POST /api/v1/users/me/password", withAuth(http.HandlerFunc(handleChangePassword), ""))
	mux.Handle("GET /api/v1/users/me/sessions", withAuth(http.HandlerFunc(handleListMySessions), ""))
	mux.Handle("DELETE /api/v1/users/me/sessions/{id}", withAuth(http.HandlerFunc(handleRevokeSession), ""))
	mux.Handle("POST /api/v1/users/me/sessions/revoke-others", withAuth(http.HandlerFunc(handleRevokeOtherSessions), ""))
	mux.Handle("POST /api/v1/auth/devices/register", withAuth(http.HandlerFunc(handleRegisterDevice), ""))
	mux.Handle("POST /api/v1/auth/ws-ticket", withAuth(http.HandlerFunc(handleWSTicket), ""))
	mux.Handle("GET /api/v1/users/me/devices", withAuth(http.HandlerFunc(handleListDevices), ""))
	mux.Handle("DELETE /api/v1/users/me/devices/{id}", withAuth(http.HandlerFunc(handleRevokeDevice), ""))

	// Settings — avatars
	mux.Handle("POST /api/v1/users/me/avatar", withAuth(http.HandlerFunc(handleUploadAvatar), ""))
	mux.Handle("DELETE /api/v1/users/me/avatar", withAuth(http.HandlerFunc(handleDeleteAvatar), ""))
	mux.Handle("GET /api/v1/users/{id}/avatar", withAuth(http.HandlerFunc(handleGetAvatar), ""))

	// Settings — admin: members
	mux.Handle("GET /api/v1/admin/users", withAuth(http.HandlerFunc(handleAdminListUsers), "manage_members"))
	mux.Handle("PUT /api/v1/admin/users/{id}/roles", withAuth(http.HandlerFunc(handleAdminSetUserRoles), "manage_roles"))
	mux.Handle("POST /api/v1/admin/users/{id}/active", withAuth(http.HandlerFunc(handleAdminSetUserActive), "manage_members"))
	mux.Handle("DELETE /api/v1/admin/users/{id}", withAuth(http.HandlerFunc(handleAdminDeleteUser), "manage_members"))

	// Settings — admin: invites
	mux.Handle("GET /api/v1/admin/invites", withAuth(http.HandlerFunc(handleAdminListInvites), "manage_community"))
	mux.Handle("DELETE /api/v1/admin/invites/{id}", withAuth(http.HandlerFunc(handleAdminRevokeInvite), "manage_community"))

	// Settings — admin: roles & permissions
	mux.Handle("POST /api/v1/admin/channels", withAuth(http.HandlerFunc(handleAdminCreateChannel), "manage_channels"))
	mux.Handle("PATCH /api/v1/admin/channels/{id}", withAuth(http.HandlerFunc(handleAdminUpdateChannel), "manage_channels"))
	mux.Handle("DELETE /api/v1/admin/channels/{id}", withAuth(http.HandlerFunc(handleAdminDeleteChannel), "manage_channels"))
	mux.Handle("GET /api/v1/admin/channels/{id}/overrides", withAuth(http.HandlerFunc(handleGetChannelOverrides), "manage_channels"))
	mux.Handle("PUT /api/v1/admin/channels/{id}/overrides", withAuth(http.HandlerFunc(handlePutChannelOverride), "manage_channels"))

	mux.Handle("GET /api/v1/admin/roles", withAuth(http.HandlerFunc(handleAdminListRoles), "manage_roles"))
	mux.Handle("GET /api/v1/admin/permissions", withAuth(http.HandlerFunc(handleAdminListPermissions), "manage_roles"))
	mux.Handle("POST /api/v1/admin/roles", withAuth(http.HandlerFunc(handleAdminCreateRole), "manage_roles"))
	mux.Handle("PUT /api/v1/admin/roles/{id}/permissions", withAuth(http.HandlerFunc(handleAdminSetRolePermissions), "manage_roles"))
	mux.Handle("DELETE /api/v1/admin/roles/{id}", withAuth(http.HandlerFunc(handleAdminDeleteRole), "manage_roles"))

	// Chat WebSocket
	mux.Handle("GET /api/v1/chat/ws", withAuth(http.HandlerFunc(handleWebSocket), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/messages", withAuth(http.HandlerFunc(handleSendMessage), "send_messages"))
	mux.Handle("GET /api/v1/channels/{id}/roots", withAuth(http.HandlerFunc(handleListChannelRoots), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/messages/{rootID}/replies", withAuth(http.HandlerFunc(handleSendReply), "send_messages"))
	mux.Handle("GET /api/v1/channels/{id}/messages/{rootID}/replies", withAuth(http.HandlerFunc(handleListThreadReplies), "view_channel"))
	mux.Handle("GET /api/v1/channels/{id}/changes", withAuth(http.HandlerFunc(handleChannelChanges), "view_channel"))
	mux.Handle("PUT /api/v1/channels/{id}/read", withAuth(http.HandlerFunc(handleMarkChannelRead), "view_channel"))
	mux.Handle("PATCH /api/v1/channels/{id}/messages/{mid}", withAuth(http.HandlerFunc(handleEditMessage), "send_messages"))
	mux.Handle("DELETE /api/v1/channels/{id}/messages/{mid}", withAuth(http.HandlerFunc(handleDeleteMessage), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/messages/{mid}/reactions", withAuth(http.HandlerFunc(handleAddReaction), "send_messages"))
	mux.Handle("DELETE /api/v1/channels/{id}/messages/{mid}/reactions/{emoji}", withAuth(http.HandlerFunc(handleRemoveReaction), "send_messages"))

	// Channel list
	mux.Handle("GET /api/v1/channels", withAuth(http.HandlerFunc(handleListChannels), "view_channel"))
	mux.Handle("GET /api/v1/channels/{id}/members", withAuth(http.HandlerFunc(handleChannelMembers), "view_channel"))
	mux.Handle("GET /api/v1/channels/{id}/pins", withAuth(http.HandlerFunc(handleListPins), "view_channel"))
	mux.Handle("POST /api/v1/channels/{id}/pins", withAuth(http.HandlerFunc(handlePinMessage), "manage_messages"))
	mux.Handle("DELETE /api/v1/channels/{id}/pins/{mid}", withAuth(http.HandlerFunc(handleUnpinMessage), "manage_messages"))
	mux.Handle("GET /api/v1/users/search", withAuth(http.HandlerFunc(handleUserSearch), "view_channel"))
	mux.Handle("GET /api/v1/search", withAuth(http.HandlerFunc(handleSearch), ""))

	// Jellyfin Proxy routes (Require view_media)
	mux.Handle("GET /api/v1/media", withAuth(http.HandlerFunc(handleMedia), "view_media"))
	mux.Handle("POST /api/v1/media/refresh", withAuth(http.HandlerFunc(handleMediaRefresh), "manage_files"))
	mux.Handle("GET /api/v1/media/refresh/status", withAuth(http.HandlerFunc(handleMediaRefreshStatus), "manage_files"))
	mux.Handle("GET /api/v1/media/items", withAuth(http.HandlerFunc(handleMediaItems), "view_media"))
	mux.Handle("GET /api/v1/media/items/{id}", withAuth(http.HandlerFunc(handleMediaItemByID), "view_media"))
	mux.Handle("GET /api/v1/media/items/{id}/cover", withAuth(http.HandlerFunc(handleMediaItemCover), "view_media"))
	mux.Handle("GET /api/v1/media/continue", withAuth(http.HandlerFunc(handleMediaContinue), "view_media"))
	mux.Handle("GET /api/v1/media/recent", withAuth(http.HandlerFunc(handleMediaRecent), "view_media"))
	mux.Handle("GET /api/v1/media/items/{id}/related", withAuth(http.HandlerFunc(handleMediaRelated), "view_media"))
	mux.Handle("GET /api/v1/media/items/{id}/playback-info", withAuth(http.HandlerFunc(handleMediaPlaybackInfo), "view_media"))
	mux.Handle("GET /api/v1/stream/audio/{id}", withAuth(http.HandlerFunc(handleStreamAudio), "view_media"))
	mux.Handle("GET /api/v1/stream/video/{id}", withAuth(http.HandlerFunc(handleStreamVideo), "view_media"))
	mux.Handle("GET /api/v1/stream/video/{id}/{path...}", withAuth(http.HandlerFunc(handleStreamVideoSubpath), "view_media"))
	mux.Handle("GET /api/v1/hls/{locator}", withAuth(http.HandlerFunc(handleHLSResource), "view_media"))
	// Library module routes (Grimmory-backed catalog)
	mux.Handle("GET /api/v1/library/books", withAuth(http.HandlerFunc(handleLibraryBooks), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}", withAuth(http.HandlerFunc(handleLibraryBookByID), "view_library"))
	mux.Handle("GET /api/v1/library/continue", withAuth(http.HandlerFunc(handleLibraryContinue), "view_library"))
	mux.Handle("GET /api/v1/library/recent", withAuth(http.HandlerFunc(handleLibraryRecent), "view_library"))
	mux.Handle("GET /api/v1/library/authors", withAuth(http.HandlerFunc(handleLibraryAuthors), "view_library"))
	mux.Handle("GET /api/v1/library/authors/{id}/books", withAuth(http.HandlerFunc(handleLibraryAuthorBooks), "view_library"))
	mux.Handle("GET /api/v1/library/series", withAuth(http.HandlerFunc(handleLibrarySeries), "view_library"))
	mux.Handle("GET /api/v1/library/series/{name}/books", withAuth(http.HandlerFunc(handleLibrarySeriesBooks), "view_library"))
	mux.Handle("GET /api/v1/library/facets", withAuth(http.HandlerFunc(handleLibraryFacets), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/cover", withAuth(http.HandlerFunc(handleLibraryBookCover), "view_library"))
	mux.Handle("GET /api/v1/library/books/{id}/content", withAuth(http.HandlerFunc(handleLibraryBookContent), "view_library"))
	mux.Handle("GET /api/v1/library/audiobooks/{id}/info", withAuth(http.HandlerFunc(handleAudiobookInfo), "view_library"))
	mux.Handle("GET /api/v1/library/audiobooks/{id}/stream", withAuth(http.HandlerFunc(handleAudiobookStream), "view_library"))
	mux.Handle("GET /api/v1/library/audiobooks/{id}/tracks/{index}/stream", withAuth(http.HandlerFunc(handleAudiobookTrackStream), "view_library"))
	// A media element cannot carry an Authorization header, so a token-mode
	// client mints a ticket and puts the authority in the URL. The GET is
	// deliberately unwrapped: the ticket is the credential, and it is verified,
	// session-checked and permission-checked inside the handler.
	mux.Handle("POST /api/v1/library/audiobooks/{id}/stream-ticket", withAuth(http.HandlerFunc(handleAudiobookStreamTicket), "view_library"))
	mux.HandleFunc("GET /api/v1/library/audiobook-stream/{ticket}", handleTicketedAudiobookStream)
	mux.Handle("GET /api/v1/library/books/{id}/progress", withAuth(http.HandlerFunc(handleGetBookProgress), "view_library"))
	mux.Handle("PUT /api/v1/library/books/{id}/progress", withAuth(http.HandlerFunc(handlePutBookProgress), "view_library"))
	mux.Handle("PUT /api/v1/library/books/{id}/metadata", withAuth(http.HandlerFunc(handleUpdateLibraryBookMetadata), "manage_library"))
	mux.Handle("POST /api/v1/library/books/{id}/metadata/fetch", withAuth(http.HandlerFunc(handleFetchLibraryBookMetadata), "manage_library"))
	mux.Handle("PUT /api/v1/library/books/{id}/cover", withAuth(http.HandlerFunc(handleUpdateLibraryBookCover), "manage_library"))
	mux.Handle("DELETE /api/v1/library/books/{id}", withAuth(http.HandlerFunc(handleDeleteLibraryBook), "manage_library"))

	// File Library (Require view_files, upload_files, upload_books, manage_files)
	mux.Handle("GET /api/v1/files", withAuth(http.HandlerFunc(handleListFiles), "view_files"))
	mux.Handle("POST /api/v1/files", withAuth(http.HandlerFunc(handleUploadFile), "upload_files"))
	mux.Handle("POST /api/v1/files/books", withAuth(http.HandlerFunc(handleUploadBook), "upload_books"))
	mux.Handle("GET /api/v1/files/{id}/download", withAuth(http.HandlerFunc(handleDownloadFile), "view_files"))
	mux.Handle("DELETE /api/v1/files/{id}", withAuth(http.HandlerFunc(handleDeleteFile), "manage_files"))
	mux.Handle("POST /api/v1/folders", withAuth(http.HandlerFunc(handleCreateFolder), "upload_files"))
	mux.Handle("DELETE /api/v1/folders/{id}", withAuth(http.HandlerFunc(handleDeleteFolder), "manage_files"))
	mux.Handle("GET /api/v1/files/audit", withAuth(http.HandlerFunc(handleListFileAudit), "manage_files"))

	// Recipient-scoped user-event stream (WebSocket catch-up + live delivery).
	mux.Handle("GET /api/v1/events", withAuth(http.HandlerFunc(handleEventsCatchUp), ""))
	mux.Handle("GET /api/v1/events/ws", withAuth(http.HandlerFunc(handleEventsWS), ""))

	// Book annotations (private/community highlights) — book-scoped, so gated on
	// view_library with the locator validated against the book format.
	mux.Handle("GET /api/v1/library/books/{id}/annotations", withAuth(http.HandlerFunc(handleListAnnotations), "view_library"))
	mux.Handle("POST /api/v1/library/books/{id}/annotations", withAuth(http.HandlerFunc(handleCreateAnnotation), "view_library"))

	// Commentary on streamed media and files — gated in-handler on view_media /
	// view_files. Comments carry no locator.
	mux.Handle("GET /api/v1/media/items/{id}/comments", withAuth(commentListHandler("media"), "view_media"))
	mux.Handle("POST /api/v1/media/items/{id}/comments", withAuth(commentCreateHandler("media"), "view_media"))
	mux.Handle("GET /api/v1/files/{id}/comments", withAuth(commentListHandler("file"), "view_files"))
	mux.Handle("POST /api/v1/files/{id}/comments", withAuth(commentCreateHandler("file"), "view_files"))

	// Annotation-scoped edit/delete/reply routes. The target type is read from
	// the row and authorized in-handler, so no fixed capability is required here.
	mux.Handle("PATCH /api/v1/library/annotations/{aid}", withAuth(http.HandlerFunc(handlePatchAnnotation), ""))
	mux.Handle("DELETE /api/v1/library/annotations/{aid}", withAuth(http.HandlerFunc(handleDeleteAnnotation), ""))
	mux.Handle("GET /api/v1/library/annotations/{aid}/replies", withAuth(http.HandlerFunc(handleListAnnotationReplies), ""))
	mux.Handle("POST /api/v1/library/annotations/{aid}/replies", withAuth(http.HandlerFunc(handleCreateAnnotationReply), ""))
	mux.Handle("DELETE /api/v1/library/annotation-replies/{rid}", withAuth(http.HandlerFunc(handleDeleteAnnotationReply), ""))
}

// gatewayHandler wraps the route table in the Internet-facing boundary chain
// (outermost first): correlation ID, response security headers, shutdown
// gating, bounded admission before any auth/DB/Redis work, CORS for the exact
// configured origins, then the exact-origin policy for state-changing requests.
// main and the route tests both build the handler here, so a test exercises the
// same chain production serves.
func gatewayHandler(mux http.Handler, cfg SecurityConfig) http.Handler {
	return requestIDMiddleware(
		securityHeaders(
			shuttingDownMiddleware(
				admissionMiddleware(
					corsMiddleware(
						requireTrustedOrigin(mux, cfg),
						cfg),
					cfg.MaxInFlightHTTP)),
			cfg))
}
