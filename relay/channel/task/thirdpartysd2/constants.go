package thirdpartysd2

var ModelList = []string{
	"dreamina-seedance-2-0-260128",
	"dreamina-seedance-2-0-fast-260128",
}

var ChannelName = "third-party-sd2"

// videoInputRatioMap 视频输入折扣比率（含视频单价 / 不含视频单价）。
// 这里沿用 doubao-seedance-2 的折扣策略，保证新渠道的计费行为尽量一致。
var videoInputRatioMap = map[string]float64{
	"dreamina-seedance-2-0-260128":      28.0 / 46.0, // ~0.6087
	"dreamina-seedance-2-0-fast-260128": 22.0 / 37.0, // ~0.5946
}

func GetVideoInputRatio(modelName string) (float64, bool) {
	r, ok := videoInputRatioMap[modelName]
	return r, ok
}
