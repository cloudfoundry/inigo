package router

const (
	FS_STATIC                   = "static"
	FS_UPLOAD_DROPLET           = "upload_droplet"
	FS_UPLOAD_BUILD_ARTIFACTS   = "upload_build_artifacts"
	FS_DOWNLOAD_BUILD_ARTIFACTS = "download_build_artifacts"
)

func NewFileServerRoutes() Routes {
	return Routes{
		{Path: "/v1/static/", Method: "GET", Handler: FS_STATIC},
		{Path: "/v1/droplet/:guid", Method: "POST", Handler: FS_UPLOAD_DROPLET},
		{Path: "/v1/build_artifacts/:app_guid", Method: "POST", Handler: FS_UPLOAD_BUILD_ARTIFACTS},
		{Path: "/v1/build_artifacts/:app_guid", Method: "GET", Handler: FS_DOWNLOAD_BUILD_ARTIFACTS},
	}
}
