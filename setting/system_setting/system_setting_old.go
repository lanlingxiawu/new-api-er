package system_setting

var ServerAddress = "http://localhost:3000"
var WorkerUrl = ""
var WorkerValidKey = ""
var WorkerAllowHttpImageRequestEnabled = false
// xiugai 添加号池节点功能
var NodeControlServiceUrl = ""
// end

func EnableWorker() bool {
	return WorkerUrl != ""
}
