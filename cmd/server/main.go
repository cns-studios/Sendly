package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"sendly/internal/config"
	"sendly/internal/handlers"
	"sendly/internal/i18n"
	"sendly/internal/middleware"
	"sendly/internal/services"
	"sendly/internal/storage"

	"github.com/gin-gonic/gin"
)

func main() {

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	db, err := storage.NewPostgres(cfg)
	if err != nil {
		log.Fatalf("Failed to connect to PostgreSQL: %v", err)
	}
	defer db.Close()
	log.Println("Connected to PostgreSQL")

	migrationCtx, migrationCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer migrationCancel()
	if err := db.RunMigrations(migrationCtx, cfg.MigrationsDir); err != nil {
		log.Fatalf("Failed to run database migrations: %v", err)
	}
	log.Println("Database migrations complete")

	rdb, err := storage.NewRedis(cfg)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer rdb.Close()
	log.Println("Connected to Redis")

	fs, err := storage.NewFilesystem(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize filesystem storage: %v", err)
	}
	log.Println("Filesystem storage initialized")

	// Claim storage before any cleanup runs: cleanup deletes whatever this
	// database doesn't know, so it must never run against another
	// instance's files.
	instanceCtx, instanceCancel := context.WithTimeout(context.Background(), 5*time.Second)
	instanceID, err := db.GetInstanceID(instanceCtx)
	instanceCancel()
	if err != nil {
		log.Fatalf("Failed to read instance ID: %v", err)
	}
	if err := fs.ClaimStorage(instanceID, cfg.AdoptDataDir); err != nil {
		log.Fatalf("Refusing to start: %v", err)
	}

	discord := services.NewDiscord(cfg)

	cleanup := services.NewCleanup(cfg, db, rdb, fs)
	go cleanup.Start()
	defer cleanup.Stop()
	log.Println("Cleanup service started")

	tracker := services.NewTracker(db)
	statsReporter := services.NewStatsReporter(cfg, db)
	go statsReporter.Start()
	defer statsReporter.Stop()
	log.Println("Stats reporter started")
	uploadService := services.NewUpload(cfg, db, rdb, fs, tracker)
	go uploadService.StartPendingCleanup()
	defer uploadService.Stop()
	log.Println("Upload service started")

	cnsClient := services.NewCNSClient(cfg)
	userCache := services.NewUserCache(cfg, db, cnsClient)
	userCache.Start()
	defer userCache.Stop()
	log.Println("User cache reconciliation service started")

	if cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}

	

	router := gin.New()
	// Only forwarding headers from configured proxies are believed; with no
	// TRUSTED_PROXIES, ClientIP is always the direct peer address.
	if err := router.SetTrustedProxies(cfg.TrustedProxies); err != nil {
		log.Fatalf("Invalid TRUSTED_PROXIES: %v", err)
	}
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	templates := template.Must(template.ParseGlob("web/templates/*.html"))
	router.SetHTMLTemplate(templates)

	translator := i18n.NewTranslator()

	ipMiddleware := middleware.NewIPMiddleware(cfg)
	standardRateLimiter, strictRateLimiter, downloadRateLimiter := middleware.NewRateLimiterSet(cfg, rdb)
	cnsAuth := middleware.CNSAuthMiddleware(cfg)

	router.Use(ipMiddleware.Handler())
	router.Use(cnsAuth)
	router.Use(middleware.UserCacheSyncMiddleware(userCache))
	router.Use(middleware.LocaleMiddleware())

	pageHandler := handlers.NewPageHandler(cfg, translator, db)
	authHandler := handlers.NewAuthHandler(cfg)
	uploadHandler := handlers.NewUploadHandler(cfg, db, rdb, fs, uploadService)
	downloadHandler := handlers.NewDownloadHandler(cfg, db, fs, tracker)
	reportHandler := handlers.NewReportHandler(cfg, db, discord)
	desktopHandler := handlers.NewDesktopHandler(cfg, db, fs, uploadService, tracker)
	androidHandler := handlers.NewAndroidHandler(cfg, db, fs, uploadService, tracker)
	recentUploadsHandler := handlers.NewRecentUploadsHandler(cfg, db)
	recentUploadsHandler.SetAndroidHub(androidHandler.Hub())
	androidHandler.SetDeviceHub(recentUploadsHandler.Hub())
	tunnelHandler := handlers.NewTunnelHandler(cfg, db, fs)

	staticFS := http.StripPrefix("/static", http.FileServer(http.Dir("./web/static")))
	serveStatic := func(c *gin.Context) {
		if c.Request.URL.Path == "/static/wordlist.txt" {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		}
		staticFS.ServeHTTP(c.Writer, c.Request)
	}
	router.GET("/static/*filepath", serveStatic)
	router.HEAD("/static/*filepath", serveStatic)

	healthHandler := handlers.NewHealthHandler(
		handlers.HealthCheck{Name: "postgres", Check: db.Ping},
		handlers.HealthCheck{Name: "redis", Check: rdb.Ping},
		handlers.HealthCheck{Name: "storage", Check: func(context.Context) error { return fs.CheckHealth(instanceID) }},
	)
	router.GET("/health", healthHandler.Health)
	router.GET("/livez", healthHandler.Live)

	router.GET("/robots.txt", pageHandler.RobotsTXT)
	router.GET("/sitemap.xml", pageHandler.Sitemap)
	router.GET("/.well-known/security.txt", pageHandler.SecurityTXT)

	router.GET("/", pageHandler.Index)
	router.GET("/quickshare", pageHandler.QuickShare)
	router.GET("/link", pageHandler.Link)
	router.GET("/tos", pageHandler.ToS)
	router.GET("/privacy", pageHandler.Privacy)
	router.GET("/limits", pageHandler.LimitsPage)
	router.GET("/data-encryption", pageHandler.DataEncryption)
	router.GET("/help", pageHandler.HelpPage)
	router.GET("/shared/:id", pageHandler.SharedFile)
	router.GET("/transfers", pageHandler.Transfers)

	auth := router.Group("/auth")
	{
		auth.GET("/login", authHandler.Login)
		auth.GET("/callback", authHandler.Callback)
		auth.GET("/logout", authHandler.Logout)
		auth.POST("/refresh", authHandler.Refresh)
	}

	api := router.Group("/api")
	api.Use(middleware.CSRFMiddleware())
	{
		api.GET("/limits", pageHandler.Limits)
		api.GET("/users/lookup", standardRateLimiter.Handler(), recentUploadsHandler.LookupUsers)
		api.GET("/users/:id/identity-key", standardRateLimiter.Handler(), recentUploadsHandler.GetUserIdentityKey)

		upload := api.Group("/upload")
		{
			upload.POST("/init", standardRateLimiter.Handler(), uploadHandler.Init)
			upload.POST("/chunk", uploadHandler.Chunk)
			upload.POST("/complete", uploadHandler.Complete)
			upload.GET("/status/:session_id", uploadHandler.AssemblyStatus)
			upload.POST("/finalize", standardRateLimiter.Handler(), uploadHandler.Finalize)
			upload.DELETE("/cancel", uploadHandler.Cancel)
		}

		file := api.Group("/file")
		{
			file.GET("/:id/download", downloadRateLimiter.Handler(), downloadHandler.Download)
			file.GET("/:id", downloadHandler.GetMetadata)
			file.GET("/code/:code", downloadHandler.GetByCode)
			file.POST("/:id/report", strictRateLimiter.Handler(), reportHandler.Report)
			file.POST("/:id/share-to-user", standardRateLimiter.Handler(), recentUploadsHandler.ShareFileToUser)
		}

		api.GET("/tunnels/:id/files/:file_id/access", tunnelHandler.GuestFileAccess)

		me := api.Group("/me")
		{
			me.GET("/recent-uploads", recentUploadsHandler.RecentUploads)
			me.GET("/shared-with-me", recentUploadsHandler.SharedWithMe)
			me.GET("/recent-share-recipients", recentUploadsHandler.RecentShareRecipients)
			me.GET("/transfers", recentUploadsHandler.ListTransfers)
			me.GET("/transfers/pending-count", recentUploadsHandler.PendingTransferCount)
			me.POST("/transfers/:file_id/accept", recentUploadsHandler.AcceptTransfer)
			me.POST("/transfers/:file_id/decline", recentUploadsHandler.DeclineTransfer)
			me.POST("/transfers/:file_id/report", strictRateLimiter.Handler(), reportHandler.ReportTransfer)
			me.GET("/files/:id/access", recentUploadsHandler.FileAccess)
			me.POST("/tunnels/start", tunnelHandler.Start)
			me.POST("/tunnels/join", strictRateLimiter.Handler(), tunnelHandler.Join)
			me.GET("/tunnels/:id", tunnelHandler.Get)
			me.GET("/tunnels/:id/participants", tunnelHandler.Participants)
			me.GET("/tunnels/:id/peer-wrap-key", tunnelHandler.PeerWrapKey)
			me.GET("/tunnels/:id/files", tunnelHandler.Files)
			me.POST("/tunnels/:id/confirm", tunnelHandler.Confirm)
			me.DELETE("/tunnels/:id", tunnelHandler.End)

			
			me.GET("/tunnels/:id/participant-keys", tunnelHandler.GetParticipantPublicKeys)
			me.POST("/tunnels/:id/envelopes", tunnelHandler.PushParticipantEnvelope)
			me.GET("/tunnels/:id/envelopes/:device_id", tunnelHandler.GetParticipantEnvelope)
			me.POST("/tunnels/:id/participants/:participant_id/approve", tunnelHandler.ApproveParticipant)
			me.POST("/tunnels/:id/participants/:participant_id/reject", tunnelHandler.RejectParticipant)

			devices := me.Group("/devices")
			{
				devices.POST("/register", recentUploadsHandler.RegisterDevice)
				devices.POST("/recover", recentUploadsHandler.RecoverDevice)
				devices.GET("/ws", recentUploadsHandler.DeviceEvents)
				devices.POST("/enrollments", strictRateLimiter.Handler(), recentUploadsHandler.CreateEnrollment)
				devices.GET("/enrollments/pending", recentUploadsHandler.ListPendingEnrollments)
				devices.POST("/enrollments/:id/approve", strictRateLimiter.Handler(), recentUploadsHandler.ApproveEnrollment)
				devices.POST("/enrollments/:id/reject", strictRateLimiter.Handler(), recentUploadsHandler.RejectEnrollment)
				devices.POST("/identity-key/envelopes", strictRateLimiter.Handler(), recentUploadsHandler.DistributeIdentityKey)
			}
		}
	}

	android := router.Group("/android")
	android.Use(middleware.AndroidAuthMiddleware(cfg))
	{
		upload := android.Group("/upload")
		{
			upload.POST("/init", standardRateLimiter.Handler(), androidHandler.UploadInit)
			upload.POST("/chunk", androidHandler.UploadChunk)
			upload.POST("/complete", androidHandler.UploadComplete)
			upload.POST("/finalize", standardRateLimiter.Handler(), androidHandler.UploadFinalize)
		}

		files := android.Group("/files")
		{
			files.GET("", androidHandler.ListFiles)
			files.GET("/:id", androidHandler.GetFile)
			files.GET("/:id/download", downloadRateLimiter.Handler(), androidHandler.Download)
		}

		me := android.Group("/me")
		{
			me.GET("/recent-uploads", androidHandler.ListFiles)
			me.GET("/files/:id/access", recentUploadsHandler.FileAccess)

			devices := me.Group("/devices")
			{
				devices.POST("/register", androidHandler.RegisterDevice)
				devices.POST("/recover", androidHandler.RecoverDevice)
				devices.GET("", androidHandler.ListConnectedDevices)
				devices.POST("/:id/rename", strictRateLimiter.Handler(), androidHandler.RenameDevice)
				devices.GET("/ws", standardRateLimiter.Handler(), androidHandler.DeviceNotificationsWS)
				devices.GET("/ws/pending-approvals", standardRateLimiter.Handler(), androidHandler.PendingApprovalsWS)
				devices.GET("/enrollments/:id/ws", standardRateLimiter.Handler(), androidHandler.WaitingForApprovalWS)
				devices.POST("/enrollments", strictRateLimiter.Handler(), androidHandler.CreateEnrollment)
				devices.GET("/enrollments/pending", androidHandler.ListPendingEnrollments)
				devices.POST("/enrollments/:id/approve", strictRateLimiter.Handler(), androidHandler.ApproveEnrollment)
				devices.POST("/enrollments/:id/reject", strictRateLimiter.Handler(), androidHandler.RejectEnrollment)
			}
		}
	}

	desktopCORS := func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, X-API-KEY, Authorization, X-Device-ID, X-Host-Token, X-Participant-Token")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}

	
	router.OPTIONS("/desktop/auth/verify", desktopCORS)
	router.OPTIONS("/desktop/auth/oauth/config", desktopCORS)
	router.OPTIONS("/desktop/auth/oauth/verify", desktopCORS)
	router.OPTIONS("/desktop/upload/init", desktopCORS)
	router.OPTIONS("/desktop/upload/chunk", desktopCORS)
	router.OPTIONS("/desktop/upload/complete", desktopCORS)
	router.OPTIONS("/desktop/upload/finalize", desktopCORS)
	router.OPTIONS("/desktop/upload/cancel", desktopCORS)
	router.OPTIONS("/desktop/upload/status/:session_id", desktopCORS)
	router.OPTIONS("/desktop/files", desktopCORS)
	router.OPTIONS("/desktop/files/:id", desktopCORS)
	router.OPTIONS("/desktop/files/:id/download", desktopCORS)
	router.OPTIONS("/desktop/file/code/:code", desktopCORS)
	router.OPTIONS("/desktop/file/:id/report", desktopCORS)
	router.OPTIONS("/desktop/me/recent-uploads", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/start", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/join", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/:id", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/:id/peer-wrap-key", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/:id/files", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/:id/confirm", desktopCORS)
	router.OPTIONS("/desktop/me/files/:id/access", desktopCORS)
	router.OPTIONS("/desktop/me/devices/register", desktopCORS)
	router.OPTIONS("/desktop/me/devices/recover", desktopCORS)
	router.OPTIONS("/desktop/me/devices/ws", desktopCORS)
	router.OPTIONS("/desktop/me/devices/enrollments", desktopCORS)
	router.OPTIONS("/desktop/me/devices/enrollments/pending", desktopCORS)
	router.OPTIONS("/desktop/me/devices/enrollments/:id/approve", desktopCORS)
	router.OPTIONS("/desktop/me/devices/enrollments/:id/reject", desktopCORS)
	
	router.OPTIONS("/desktop/me/tunnels/:id/participant-keys", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/:id/envelopes", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/:id/envelopes/:device_id", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/:id/participants/:participant_id/approve", desktopCORS)
	router.OPTIONS("/desktop/me/tunnels/:id/participants/:participant_id/reject", desktopCORS)

	desktop := router.Group("/desktop")
	desktop.Use(desktopCORS)
	{
		desktop.GET("/auth/verify", desktopHandler.VerifyKey)
		desktop.GET("/auth/oauth/config", desktopHandler.OAuthConfig)
		desktop.GET("/auth/oauth/verify", desktopHandler.OAuthVerify)
		desktop.GET("/ws", standardRateLimiter.Handler(), desktopHandler.WebSocket)
		desktop.GET("/limits", pageHandler.Limits)

		desktopAuth := desktop.Group("")
		desktopAuth.Use(middleware.DesktopAuthMiddleware(cfg, db))
		{
			upload := desktopAuth.Group("/upload")
			{
				upload.POST("/init", desktopHandler.UploadInit)
				upload.POST("/chunk", desktopHandler.UploadChunk)
				upload.POST("/complete", desktopHandler.UploadComplete)
				upload.POST("/finalize", standardRateLimiter.Handler(), desktopHandler.UploadFinalize)
				upload.GET("/status/:session_id", desktopHandler.UploadStatus)
				upload.DELETE("/cancel", uploadHandler.Cancel)
			}

			files := desktopAuth.Group("/files")
			{
				files.GET("", desktopHandler.ListFiles)
				files.GET("/:id", desktopHandler.GetFile)
				files.GET("/:id/download", downloadRateLimiter.Handler(), desktopHandler.DownloadFile)
			}

			file := desktopAuth.Group("/file")
			{
				file.GET("/code/:code", downloadHandler.GetByCode)
				file.POST("/:id/report", strictRateLimiter.Handler(), reportHandler.Report)
			}

			me := desktopAuth.Group("/me")
			{
				me.GET("/recent-uploads", recentUploadsHandler.RecentUploads)
				me.GET("/files/:id/access", recentUploadsHandler.FileAccess)
				me.POST("/tunnels/start", tunnelHandler.Start)
				me.POST("/tunnels/join", strictRateLimiter.Handler(), tunnelHandler.Join)
				me.GET("/tunnels/:id", tunnelHandler.Get)
				me.GET("/tunnels/:id/participants", tunnelHandler.Participants)
				me.GET("/tunnels/:id/peer-wrap-key", tunnelHandler.PeerWrapKey)
				me.GET("/tunnels/:id/files", tunnelHandler.Files)
				me.POST("/tunnels/:id/confirm", tunnelHandler.Confirm)
				me.DELETE("/tunnels/:id", tunnelHandler.End)

				
				me.GET("/tunnels/:id/participant-keys", tunnelHandler.GetParticipantPublicKeys)
				me.POST("/tunnels/:id/envelopes", tunnelHandler.PushParticipantEnvelope)
				me.GET("/tunnels/:id/envelopes/:device_id", tunnelHandler.GetParticipantEnvelope)
				me.POST("/tunnels/:id/participants/:participant_id/approve", tunnelHandler.ApproveParticipant)
				me.POST("/tunnels/:id/participants/:participant_id/reject", tunnelHandler.RejectParticipant)

				devices := me.Group("/devices")
				{
					devices.POST("/register", recentUploadsHandler.RegisterDevice)
					devices.POST("/recover", recentUploadsHandler.RecoverDevice)
					devices.GET("/ws", recentUploadsHandler.DeviceEvents)
					devices.POST("/enrollments", strictRateLimiter.Handler(), recentUploadsHandler.CreateEnrollment)
					devices.GET("/enrollments/pending", recentUploadsHandler.ListPendingEnrollments)
					devices.POST("/enrollments/:id/approve", strictRateLimiter.Handler(), recentUploadsHandler.ApproveEnrollment)
					devices.POST("/enrollments/:id/reject", strictRateLimiter.Handler(), recentUploadsHandler.RejectEnrollment)
				}
			}
		}
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 30 * time.Second,
		ReadTimeout:       0,
		WriteTimeout:      0,
		IdleTimeout:       300 * time.Second,
	}

	go func() {
		log.Printf("Server starting on port %s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server exited gracefully")
}