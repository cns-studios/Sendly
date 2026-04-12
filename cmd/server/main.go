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
			upload.POST("/finalize", uploadHandler.Finalize)
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

			devices := me.Group("/devices")
			{
				devices.POST("/register", recentUploadsHandler.RegisterDevice)
				devices.POST("/recover", rateLimiter.Handler(), recentUploadsHandler.RecoverDevice)
				devices.GET("/ws", recentUploadsHandler.DeviceEvents)
				devices.POST("/enrollments", rateLimiter.Handler(), recentUploadsHandler.CreateEnrollment)
				devices.GET("/enrollments/pending", recentUploadsHandler.ListPendingEnrollments)
				devices.POST("/enrollments/:id/approve", rateLimiter.Handler(), recentUploadsHandler.ApproveEnrollment)
				devices.POST("/enrollments/:id/reject", rateLimiter.Handler(), recentUploadsHandler.RejectEnrollment)
			}
		}
	}

	desktopCORS := func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type, X-API-KEY")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}

	router.OPTIONS("/desktop/auth/verify", desktopCORS)
	router.OPTIONS("/desktop/upload/init", desktopCORS)
	router.OPTIONS("/desktop/upload/chunk", desktopCORS)
	router.OPTIONS("/desktop/upload/complete", desktopCORS)
	router.OPTIONS("/desktop/upload/finalize", desktopCORS)
	router.OPTIONS("/desktop/upload/status/:session_id", desktopCORS)
	router.OPTIONS("/desktop/files", desktopCORS)
	router.OPTIONS("/desktop/files/:id", desktopCORS)
	router.OPTIONS("/desktop/files/:id/download", desktopCORS)

	desktop := router.Group("/desktop")
	desktop.Use(desktopCORS)
	{
		desktop.GET("/auth/verify", desktopHandler.VerifyKey)
		desktop.GET("/ws", desktopHandler.WebSocket)

		desktopAuth := desktop.Group("")
		desktopAuth.Use(middleware.DesktopAuthMiddleware(db))
		{
			upload := desktopAuth.Group("/upload")
			{
				upload.POST("/init", desktopHandler.UploadInit)
				upload.POST("/chunk", desktopHandler.UploadChunk)
				upload.POST("/complete", desktopHandler.UploadComplete)
				upload.POST("/finalize", desktopHandler.UploadFinalize)
				upload.GET("/status/:session_id", desktopHandler.UploadStatus)
			}

			files := desktopAuth.Group("/files")
			{
				files.GET("", desktopHandler.ListFiles)
				files.GET("/:id", desktopHandler.GetFile)
				files.GET("/:id/download", desktopHandler.DownloadFile)
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
