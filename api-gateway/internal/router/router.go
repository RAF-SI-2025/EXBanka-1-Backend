package router

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	authpb "github.com/exbanka/contract/authpb"
	userpb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/api-gateway/internal/handler"
	"github.com/exbanka/api-gateway/internal/middleware"
)

func Setup(authClient authpb.AuthServiceClient, userClient userpb.UserServiceClient) *gin.Engine {
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	authHandler := handler.NewAuthHandler(authClient)
	empHandler := handler.NewEmployeeHandler(userClient, authClient)

	api := r.Group("/api")
	{
		// Public auth routes
		auth := api.Group("/auth")
		{
			auth.POST("/login", authHandler.Login)
			auth.POST("/refresh", authHandler.RefreshToken)
			auth.POST("/logout", authHandler.Logout)
			auth.POST("/password/reset-request", authHandler.RequestPasswordReset)
			auth.POST("/password/reset", authHandler.ResetPassword)
			auth.POST("/activate", authHandler.ActivateAccount)
		}

		// Protected routes
		protected := api.Group("/")
		protected.Use(middleware.AuthMiddleware(authClient))
		{
			employees := protected.Group("/employees")
			employees.Use(middleware.RequirePermission("employees.read"))
			{
				employees.GET("", empHandler.ListEmployees)
				employees.GET("/:id", empHandler.GetEmployee)
			}

			adminEmployees := protected.Group("/employees")
			adminEmployees.Use(middleware.RequirePermission("employees.create"))
			{
				adminEmployees.POST("", empHandler.CreateEmployee)
			}

			updateEmployees := protected.Group("/employees")
			updateEmployees.Use(middleware.RequirePermission("employees.update"))
			{
				updateEmployees.PUT("/:id", empHandler.UpdateEmployee)
			}
		}
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	return r
}
