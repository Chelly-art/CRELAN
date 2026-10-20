package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	assets "crelan"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	auth, err := newAuthService()
	if err != nil {
		log.Fatalf("initialize authentication: %v", err)
	}
	defer auth.db.Close()

	staticFiles, err := fs.Sub(assets.Web, "web")
	if err != nil {
		log.Fatalf("load web assets: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", healthHandler)
	auth.registerRoutes(mux)
	githubApp, err := newGitHubAppService(auth.db, auth)
	if err != nil {
		log.Fatalf("configure GitHub App integration: %v", err)
	}
	githubApp.registerRoutes(mux)
	var provisioner DeploymentProvisioner
	clusterProvisioner, err := newKubernetesProvisioner()
	if err != nil {
		if errors.Is(err, errKubernetesNotConfigured) {
			log.Printf("Kubernetes deployment provisioner disabled: %v", err)
		} else {
			log.Printf("Kubernetes deployment provisioner unavailable: %v", err)
		}
	} else {
		provisioner = clusterProvisioner
		log.Printf("Kubernetes deployment provisioner configured")
	}
	var runtime deploymentRuntime
	if cluster, ok := provisioner.(deploymentRuntime); ok {
		runtime = cluster
	}
	deployments := &deploymentService{
		db: auth.db, auth: auth, provisioner: provisioner, runtime: runtime, githubApp: githubApp,
	}
	githubApp.deployments = deployments
	deployments.registerRoutes(mux)
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	if provisioner != nil {
		go func() {
			if err := deployments.runWorkers(workerCtx, 2); err != nil {
				log.Printf("deployment workers stopped: %v", err)
			}
		}()
	}
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(staticFiles))))

	host := ""
	if auth.demoLoginEnabled {
		host = "127.0.0.1"
		log.Printf("Local demo login enabled; binding to loopback only")
	}
	server := &http.Server{
		Addr:              host + ":" + port,
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown: %v", err)
		}
	}()

	log.Printf("Crelan development server listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("start server: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
