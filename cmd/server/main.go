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

	"shareit/internal/config"
	"shareit/internal/handlers"
	"shareit/internal/middleware"
	"shareit/internal/services"
	"shareit/internal/storage"

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

	discord := services.NewDiscord(cfg)

	cleanup := services.NewCleanup(cfg, db, rdb, fs)
	go cleanup.Start()
	defer cleanup.Stop()
	log.Println("Cleanup service started")

	uploadService := services.NewUpload(cfg, db, rdb, fs)
	go uploadService.StartPendingCleanup()
	defer uploadService.Stop()
	log.Println("Upload service started")

	if cfg.IsProd() {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	router.Use(func(c *gin.Context) {
		c.Header("Connection", "keep-alive")
		c.Header("Keep-Alive", "timeout=55")
		c.Next()
	})

	templates := template.Must(template.ParseGlob("web/templates/*.html"))
	router.SetHTMLTemplate(templates)

	ipMiddleware := middleware.NewIPMiddleware(cfg)
	rateLimiter := middleware.NewRateLimiter(rdb)
	cnsAuth := middleware.CNSAuthMiddleware(cfg)

	router.Use(ipMiddleware.Handler())
	router.Use(cnsAuth)

	pageHandler := handlers.NewPageHandler(cfg)
	authHandler := handlers.NewAuthHandler(cfg)
	uploadHandler := handlers.NewUploadHandler(cfg, db, rdb, fs, uploadService)
	downloadHandler := handlers.NewDownloadHandler(cfg, db, fs)
	reportHandler := handlers.NewReportHandler(cfg, db, discord)
	desktopHandler := handlers.NewDesktopHandler(cfg, db, fs, uploadService)
	recentUploadsHandler := handlers.NewRecentUploadsHandler(cfg, db)
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

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	router.GET("/", pageHandler.Index)
	router.GET("/tos", pageHandler.ToS)
	router.GET("/privacy", pageHandler.Privacy)
	router.GET("/shared", pageHandler.SharedLookup)
	router.GET("/shared/:id", pageHandler.SharedFile)

	auth := router.Group("/auth")
	{
		auth.GET("/login", authHandler.Login)
		auth.GET("/callback", authHandler.Callback)
		auth.GET("/logout", authHandler.Logout)
	}

	api := router.Group("/api")
	api.Use(middleware.CSRFMiddleware())
	{
		api.GET("/limits", pageHandler.Limits)

		upload := api.Group("/upload")
		{
			upload.POST("/init", rateLimiter.Handler(), uploadHandler.Init)
			upload.POST("/chunk", uploadHandler.Chunk)
			upload.POST("/complete", uploadHandler.Complete)
			upload.GET("/status/:session_id", uploadHandler.AssemblyStatus)
			upload.POST("/finalize", rateLimiter.Handler(), uploadHandler.Finalize)
			upload.DELETE("/cancel", uploadHandler.Cancel)
		}

		file := api.Group("/file")
		{
			file.GET("/:id", downloadHandler.GetMetadata)
			file.GET("/:id/download", downloadHandler.Download)
			file.GET("/code/:code", downloadHandler.GetByCode)
			file.POST("/:id/report", reportHandler.Report)
		}

		me := api.Group("/me")
		{
			me.GET("/recent-uploads", recentUploadsHandler.RecentUploads)
			me.GET("/files/:id/access", recentUploadsHandler.FileAccess)
			me.POST("/tunnels/start", tunnelHandler.Start)
			me.POST("/tunnels/join", tunnelHandler.Join)
			me.GET("/tunnels/:id", tunnelHandler.Get)
			me.GET("/tunnels/:id/files", tunnelHandler.Files)
			me.POST("/tunnels/:id/confirm", tunnelHandler.Confirm)
			me.DELETE("/tunnels/:id", tunnelHandler.End)

			devices := me.Group("/devices")
			{
				devices.POST("/register", recentUploadsHandler.RegisterDevice)
				devices.POST("/recover", recentUploadsHandler.RecoverDevice)
				devices.GET("/ws", recentUploadsHandler.DeviceEvents)
				devices.POST("/enrollments", recentUploadsHandler.CreateEnrollment)
				devices.GET("/enrollments/pending", recentUploadsHandler.ListPendingEnrollments)
				devices.POST("/enrollments/:id/approve", recentUploadsHandler.ApproveEnrollment)
				devices.POST("/enrollments/:id/reject", recentUploadsHandler.RejectEnrollment)
			}
		}
	}

	desktopCORS := func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, X-API-KEY, Authorization")
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

	desktop := router.Group("/desktop")
	desktop.Use(desktopCORS)
	{
		desktop.GET("/auth/verify", desktopHandler.VerifyKey)
		desktop.GET("/auth/oauth/config", desktopHandler.OAuthConfig)
		desktop.GET("/auth/oauth/verify", desktopHandler.OAuthVerify)
		desktop.GET("/ws", desktopHandler.WebSocket)
		desktop.GET("/limits", pageHandler.Limits)

		desktopAuth := desktop.Group("")
		desktopAuth.Use(middleware.DesktopAuthMiddleware(cfg, db))
		{
			upload := desktopAuth.Group("/upload")
			{
				upload.POST("/init", desktopHandler.UploadInit)
				upload.POST("/chunk", desktopHandler.UploadChunk)
				upload.POST("/complete", desktopHandler.UploadComplete)
				upload.POST("/finalize", rateLimiter.Handler(), desktopHandler.UploadFinalize)
				upload.GET("/status/:session_id", desktopHandler.UploadStatus)
				upload.DELETE("/cancel", uploadHandler.Cancel)
			}

			files := desktopAuth.Group("/files")
			{
				files.GET("", desktopHandler.ListFiles)
				files.GET("/:id", desktopHandler.GetFile)
				files.GET("/:id/download", desktopHandler.DownloadFile)
			}

			file := desktopAuth.Group("/file")
			{
				file.GET("/code/:code", downloadHandler.GetByCode)
				file.POST("/:id/report", reportHandler.Report)
			}

			me := desktopAuth.Group("/me")
			{
				me.GET("/recent-uploads", recentUploadsHandler.RecentUploads)
				me.GET("/files/:id/access", recentUploadsHandler.FileAccess)
				me.POST("/tunnels/start", tunnelHandler.Start)
				me.POST("/tunnels/join", tunnelHandler.Join)
				me.GET("/tunnels/:id", tunnelHandler.Get)
				me.GET("/tunnels/:id/files", tunnelHandler.Files)
				me.POST("/tunnels/:id/confirm", tunnelHandler.Confirm)
				me.DELETE("/tunnels/:id", tunnelHandler.End)

				devices := me.Group("/devices")
				{
					devices.POST("/register", recentUploadsHandler.RegisterDevice)
					devices.POST("/recover", recentUploadsHandler.RecoverDevice)
					devices.GET("/ws", recentUploadsHandler.DeviceEvents)
					devices.POST("/enrollments", recentUploadsHandler.CreateEnrollment)
					devices.GET("/enrollments/pending", recentUploadsHandler.ListPendingEnrollments)
					devices.POST("/enrollments/:id/approve", recentUploadsHandler.ApproveEnrollment)
					devices.POST("/enrollments/:id/reject", recentUploadsHandler.RejectEnrollment)
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
		IdleTimeout:       60 * time.Second,
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
