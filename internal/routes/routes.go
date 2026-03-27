package routes

import (
	"net/http"
	"reflect"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type Route struct {
	Name   string
	Engine *gin.Engine
}

func (r *Route) Init(engine *gin.Engine) {
	r.Engine = engine
}

type allRoutes struct {
	log *zap.Logger
}

// Self-Ping function: Ye Render ko 24/7 "Awake" rakhega
func keepAlive(url string, log *zap.Logger) {
	ticker := time.NewTicker(10 * time.Minute) // Har 10 minute mein hit karega
	for range ticker.C {
		resp, err := http.Get(url)
		if err != nil {
			log.Error("Self-ping failed", zap.Error(err))
			continue
		}
		resp.Body.Close()
		log.Info("Self-ping successful", zap.Int("status", resp.StatusCode))
	}
}

func Load(log *zap.Logger, r *gin.Engine) {
	log = log.Named("routes")
	defer log.Sugar().Info("Loaded all API Routes")

	// ── Ping endpoints ──
	
	// Standard ping
	r.GET("/ping", func(ctx *gin.Context) {
		ctx.String(200, "pong")
	})
	r.HEAD("/ping", func(ctx *gin.Context) {
		ctx.Status(200)
	})

	// Extra route: Logs mein jo 404 aa raha tha usse fix karne ke liye
	r.GET("/stream/ping", func(ctx *gin.Context) {
		ctx.String(200, "pong")
	})
	r.HEAD("/stream/ping", func(ctx *gin.Context) {
		ctx.Status(200)
	})

	// Start Self-Ping in background
	// Apni Render URL yahan check kar lein
	go keepAlive("https://renbotstream.onrender.com/ping", log)

	route := &Route{Name: "/", Engine: r}
	route.Init(r)
	
	Type := reflect.TypeOf(&allRoutes{log})
	Value := reflect.ValueOf(&allRoutes{log})
	
	for i := 0; i < Type.NumMethod(); i++ {
		Type.Method(i).Func.Call([]reflect.Value{Value, reflect.ValueOf(route)})
	}
}
