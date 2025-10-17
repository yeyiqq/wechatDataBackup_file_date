package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"wechatDataBackup/pkg/utils"
	"wechatDataBackup/pkg/wechat"

	"github.com/spf13/viper"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	defaultConfig        = "config"
	configDefaultUserKey = "userConfig.defaultUser"
	configUsersKey       = "userConfig.users"
	configExportPathKey  = "exportPath"
	appVersion           = "v1.2.4"
)

type FileLoader struct {
	http.Handler
	FilePrefix string
}

func NewFileLoader(prefix string) *FileLoader {
	mime.AddExtensionType(".mp3", "audio/mpeg")
	return &FileLoader{FilePrefix: prefix}
}

func (h *FileLoader) SetFilePrefix(prefix string) {
	h.FilePrefix = prefix
	log.Println("SetFilePrefix", h.FilePrefix)
}

func (h *FileLoader) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	requestedFilename := h.FilePrefix + "\\" + strings.TrimPrefix(req.URL.Path, "/")

	file, err := os.Open(requestedFilename)
	if err != nil {
		http.Error(res, fmt.Sprintf("Could not load file %s", requestedFilename), http.StatusBadRequest)
		return
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		http.Error(res, "Could not retrieve file info", http.StatusInternalServerError)
		return
	}

	fileSize := fileInfo.Size()
	rangeHeader := req.Header.Get("Range")
	if rangeHeader == "" {
		// 无 Range 请求，直接返回整个文件
		res.Header().Set("Content-Length", strconv.FormatInt(fileSize, 10))
		http.ServeContent(res, req, requestedFilename, fileInfo.ModTime(), file)
		return
	}

	var start, end int64
	if strings.HasPrefix(rangeHeader, "bytes=") {
		ranges := strings.Split(strings.TrimPrefix(rangeHeader, "bytes="), "-")
		start, _ = strconv.ParseInt(ranges[0], 10, 64)

		if len(ranges) > 1 && ranges[1] != "" {
			end, _ = strconv.ParseInt(ranges[1], 10, 64)
		} else {
			end = fileSize - 1
		}
	} else {
		http.Error(res, "Invalid Range header", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	if start < 0 || end >= fileSize || start > end {
		http.Error(res, "Requested range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	contentType := mime.TypeByExtension(filepath.Ext(requestedFilename))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	res.Header().Set("Content-Type", contentType)
	res.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, fileSize))
	res.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	res.WriteHeader(http.StatusPartialContent)
	buffer := make([]byte, 102400)
	file.Seek(start, 0)
	for current := start; current <= end; {
		readSize := int64(len(buffer))
		if end-current+1 < readSize {
			readSize = end - current + 1
		}

		n, err := file.Read(buffer[:readSize])
		if err != nil {
			break
		}

		res.Write(buffer[:n])
		current += int64(n)
	}
}

// App struct
type App struct {
	ctx         context.Context
	infoList    *wechat.WeChatInfoList
	provider    *wechat.WechatDataProvider
	defaultUser string
	users       []string
	firstStart  bool
	firstInit   bool
	FLoader     *FileLoader
}

type WeChatInfo struct {
	ProcessID  uint32 `json:"PID"`
	FilePath   string `json:"FilePath"`
	AcountName string `json:"AcountName"`
	Version    string `json:"Version"`
	Is64Bits   bool   `json:"Is64Bits"`
	DBKey      string `json:"DBkey"`
}

type WeChatInfoList struct {
	Info  []WeChatInfo `json:"Info"`
	Total int          `json:"Total"`
}

type WeChatAccountInfos struct {
	CurrentAccount string                     `json:"CurrentAccount"`
	Info           []wechat.WeChatAccountInfo `json:"Info"`
	Total          int                        `json:"Total"`
}

type ErrorMessage struct {
	ErrorStr string `json:"error"`
}

// 增量备份配置
type IncrementalBackupConfig struct {
	EnableBackup    bool   `json:"enableBackup"`
	BackupPath      string `json:"backupPath"`
	LastBackupTime  int64  `json:"lastBackupTime"`
	MaxBackupVersions int  `json:"maxBackupVersions"`
}

// 新增数据记录
type NewDataRecord struct {
	FilePath    string `json:"filePath"`
	FileSize    int64  `json:"fileSize"`
	ModifyTime  int64  `json:"modifyTime"`
	FileHash    string `json:"fileHash"`
	DataType    string `json:"dataType"` // "database", "image", "video", "voice", etc.
	BackupPath  string `json:"backupPath"`
}

// 增量备份结果
type IncrementalBackupResult struct {
	TotalFiles     int             `json:"totalFiles"`
	NewFiles       int             `json:"newFiles"`
	BackupFiles    int             `json:"backupFiles"`
	BackupSize     int64           `json:"backupSize"`
	BackupPath     string          `json:"backupPath"`
	NewDataRecords []NewDataRecord `json:"newDataRecords"`
}

// 新消息导出配置
type NewMessageExportConfig struct {
	EnableExport    bool  `json:"enableExport"`
	StartTime       int64 `json:"startTime"`       // 开始时间戳（2025-10-16 00:00:00）
	SavePath        string `json:"savePath"`       // 保存路径
	IncludeMedia    bool  `json:"includeMedia"`    // 是否包含媒体文件
	GroupByContact  bool  `json:"groupByContact"`  // 按联系人分组
}

// 对话消息结构
type DialogueMessage struct {
	Index   int    `json:"index"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
	Time    string `json:"time"`
}

// 对话组结构
type DialogueGroup struct {
	Instruction string            `json:"instruction"`
	Dialogue    []DialogueMessage `json:"dialogue"`
}

// 新消息导出结果
type NewMessageExportResult struct {
	TotalContacts int                    `json:"totalContacts"`
	TotalMessages int                    `json:"totalMessages"`
	SavePath      string                 `json:"savePath"`
	Contacts      []ContactMessageData   `json:"contacts"`
	ExportTime    string                 `json:"exportTime"`
}

// 联系人消息数据
type ContactMessageData struct {
	ContactName string         `json:"contactName"`
	MessageCount int           `json:"messageCount"`
	FilePath    string         `json:"filePath"`
	Dialogue    []DialogueGroup `json:"dialogue"`
}

// NewApp creates a new App application struct
func NewApp() *App {
	a := &App{}
	log.Println("App version:", appVersion)
	a.firstInit = true
	a.FLoader = NewFileLoader(".\\")
	viper.SetConfigName(defaultConfig)
	viper.SetConfigType("json")
	viper.AddConfigPath(".")
	if err := viper.ReadInConfig(); err == nil {
		a.defaultUser = viper.GetString(configDefaultUserKey)
		a.users = viper.GetStringSlice(configUsersKey)
		prefix := viper.GetString(configExportPathKey)
		if prefix != "" {
			log.Println("SetFilePrefix", prefix)
			a.FLoader.SetFilePrefix(prefix)
		}
	} else {
		log.Println("not config exist")
	}
	log.Printf("default: %s users: %v\n", a.defaultUser, a.users)
	if len(a.users) == 0 {
		a.firstStart = true
	}

	return a
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) beforeClose(ctx context.Context) (prevent bool) {
	return false
}

func (a *App) shutdown(ctx context.Context) {
	if a.provider != nil {
		a.provider.WechatWechatDataProviderClose()
		a.provider = nil
	}
	log.Printf("App Version %s exit!", appVersion)
}

func (a *App) GetWeChatAllInfo() string {
	infoList := WeChatInfoList{}
	infoList.Info = make([]WeChatInfo, 0)
	infoList.Total = 0

	if a.provider != nil {
		a.provider.WechatWechatDataProviderClose()
		a.provider = nil
	}

	a.infoList = wechat.GetWeChatAllInfo()
	for i := range a.infoList.Info {
		var info WeChatInfo
		info.ProcessID = a.infoList.Info[i].ProcessID
		info.FilePath = a.infoList.Info[i].FilePath
		info.AcountName = a.infoList.Info[i].AcountName
		info.Version = a.infoList.Info[i].Version
		info.Is64Bits = a.infoList.Info[i].Is64Bits
		info.DBKey = a.infoList.Info[i].DBKey
		infoList.Info = append(infoList.Info, info)
		infoList.Total += 1
		log.Printf("ProcessID %d, FilePath %s, AcountName %s, Version %s, Is64Bits %t", info.ProcessID, info.FilePath, info.AcountName, info.Version, info.Is64Bits)
	}
	infoStr, _ := json.Marshal(infoList)
	// log.Println(string(infoStr))

	return string(infoStr)
}

func (a *App) ExportWeChatAllData(full bool, acountName string) {

	if a.provider != nil {
		a.provider.WechatWechatDataProviderClose()
		a.provider = nil
	}

	progress := make(chan string)
	go func() {
		var pInfo *wechat.WeChatInfo
		for i := range a.infoList.Info {
			if a.infoList.Info[i].AcountName == acountName {
				pInfo = &a.infoList.Info[i]
				break
			}
		}

		if pInfo == nil {
			close(progress)
			runtime.EventsEmit(a.ctx, "exportData", fmt.Sprintf("{\"status\":\"error\", \"result\":\"%s error\"}", acountName))
			return
		}

		prefixExportPath := a.FLoader.FilePrefix + "\\User\\"
		_, err := os.Stat(prefixExportPath)
		if err != nil {
			os.Mkdir(prefixExportPath, os.ModeDir)
		}

		expPath := prefixExportPath + pInfo.AcountName
		_, err = os.Stat(expPath)
		if err == nil {
			if !full {
				os.RemoveAll(expPath + "\\Msg")
			} else {
				os.RemoveAll(expPath)
			}
		}

		_, err = os.Stat(expPath)
		if err != nil {
			os.Mkdir(expPath, os.ModeDir)
		}

		go wechat.ExportWeChatAllData(*pInfo, expPath, progress)

		for p := range progress {
			log.Println(p)
			runtime.EventsEmit(a.ctx, "exportData", p)
		}

		// 导出完成后，执行新消息导出（仅增量导出时）
		log.Println("开始检查是否需要导出新消息，full=", full)
		if !full {
			log.Println("执行新消息导出，账号名=", pInfo.AcountName, "导出路径=", expPath)
			newMessageResult := a.exportNewMessages(pInfo.AcountName, expPath)
			if newMessageResult != nil {
				log.Println("新消息导出完成，结果=", newMessageResult)
				// 发送新消息导出结果
				resultJson, _ := json.Marshal(newMessageResult)
				runtime.EventsEmit(a.ctx, "newMessageExport", string(resultJson))
			} else {
				log.Println("新消息导出返回nil结果")
			}
		} else {
			log.Println("跳过新消息导出，因为这是全量导出")
		}

		a.defaultUser = pInfo.AcountName
		hasUser := false
		for _, user := range a.users {
			if user == pInfo.AcountName {
				hasUser = true
				break
			}
		}
		if !hasUser {
			a.users = append(a.users, pInfo.AcountName)
		}
		a.setCurrentConfig()
	}()
}

func (a *App) createWechatDataProvider(resPath string, prefix string) error {
	if a.provider != nil && a.provider.SelfInfo != nil && filepath.Base(resPath) == a.provider.SelfInfo.UserName {
		log.Println("WechatDataProvider not need create:", a.provider.SelfInfo.UserName)
		return nil
	}

	if a.provider != nil {
		a.provider.WechatWechatDataProviderClose()
		a.provider = nil
		log.Println("createWechatDataProvider WechatWechatDataProviderClose")
	}

	provider, err := wechat.CreateWechatDataProvider(resPath, prefix)
	if err != nil {
		log.Println("CreateWechatDataProvider failed:", resPath)
		return err
	}

	a.provider = provider
	// infoJson, _ := json.Marshal(a.provider.SelfInfo)
	// runtime.EventsEmit(a.ctx, "selfInfo", string(infoJson))
	return nil
}

func (a *App) WeChatInit() {

	if a.firstInit {
		a.firstInit = false
		a.scanAccountByPath(a.FLoader.FilePrefix)
		log.Println("scanAccountByPath:", a.FLoader.FilePrefix)
	}

	if len(a.defaultUser) == 0 {
		log.Println("not defaultUser")
		return
	}

	expPath := a.FLoader.FilePrefix + "\\User\\" + a.defaultUser
	prefixPath := "\\User\\" + a.defaultUser
	wechat.ExportWeChatHeadImage(expPath)
	if a.createWechatDataProvider(expPath, prefixPath) == nil {
		infoJson, _ := json.Marshal(a.provider.SelfInfo)
		runtime.EventsEmit(a.ctx, "selfInfo", string(infoJson))
	}
}

func (a *App) GetWechatSessionList(pageIndex int, pageSize int) string {
	if a.provider == nil {
		log.Println("provider not init")
		return "{\"Total\":0}"
	}
	log.Printf("pageIndex: %d\n", pageIndex)
	list, err := a.provider.WeChatGetSessionList(pageIndex, pageSize)
	if err != nil {
		return "{\"Total\":0}"
	}

	listStr, _ := json.Marshal(list)
	log.Println("GetWechatSessionList:", list.Total)
	return string(listStr)
}

func (a *App) GetWechatContactList(pageIndex int, pageSize int) string {
	if a.provider == nil {
		log.Println("provider not init")
		return "{\"Total\":0}"
	}
	log.Printf("pageIndex: %d\n", pageIndex)
	list, err := a.provider.WeChatGetContactList(pageIndex, pageSize)
	if err != nil {
		return "{\"Total\":0}"
	}

	listStr, _ := json.Marshal(list)
	log.Println("WeChatGetContactList:", list.Total)
	return string(listStr)
}

func (a *App) GetWechatMessageListByTime(userName string, time int64, pageSize int, direction string) string {
	log.Println("GetWechatMessageListByTime:", userName, pageSize, time, direction)
	if len(userName) == 0 {
		return "{\"Total\":0, \"Rows\":[]}"
	}
	dire := wechat.Message_Search_Forward
	if direction == "backward" {
		dire = wechat.Message_Search_Backward
	} else if direction == "both" {
		dire = wechat.Message_Search_Both
	}
	list, err := a.provider.WeChatGetMessageListByTime(userName, time, pageSize, dire)
	if err != nil {
		log.Println("GetWechatMessageListByTime failed:", err)
		return ""
	}
	listStr, _ := json.Marshal(list)
	log.Println("GetWechatMessageListByTime:", list.Total)

	return string(listStr)
}

func (a *App) GetWechatMessageListByType(userName string, time int64, pageSize int, msgType string, direction string) string {
	log.Println("GetWechatMessageListByType:", userName, pageSize, time, msgType, direction)
	if len(userName) == 0 {
		return "{\"Total\":0, \"Rows\":[]}"
	}
	dire := wechat.Message_Search_Forward
	if direction == "backward" {
		dire = wechat.Message_Search_Backward
	} else if direction == "both" {
		dire = wechat.Message_Search_Both
	}
	list, err := a.provider.WeChatGetMessageListByType(userName, time, pageSize, msgType, dire)
	if err != nil {
		log.Println("WeChatGetMessageListByType failed:", err)
		return ""
	}
	listStr, _ := json.Marshal(list)
	log.Println("WeChatGetMessageListByType:", list.Total)

	return string(listStr)
}

func (a *App) GetWechatMessageListByKeyWord(userName string, time int64, keyword string, msgType string, pageSize int) string {
	log.Println("GetWechatMessageListByKeyWord:", userName, pageSize, time, msgType)
	if len(userName) == 0 {
		return "{\"Total\":0, \"Rows\":[]}"
	}
	list, err := a.provider.WeChatGetMessageListByKeyWord(userName, time, keyword, msgType, pageSize)
	if err != nil {
		log.Println("WeChatGetMessageListByKeyWord failed:", err)
		return ""
	}
	listStr, _ := json.Marshal(list)
	log.Println("WeChatGetMessageListByKeyWord:", list.Total, list.KeyWord)

	return string(listStr)
}

func (a *App) GetWechatMessageDate(userName string) string {
	log.Println("GetWechatMessageDate:", userName)
	if len(userName) == 0 {
		return "{\"Total\":0, \"Date\":[]}"
	}

	messageData, err := a.provider.WeChatGetMessageDate(userName)
	if err != nil {
		log.Println("GetWechatMessageDate:", err)
		return ""
	}

	messageDataStr, _ := json.Marshal(messageData)
	log.Println("GetWechatMessageDate:", messageData.Total)

	return string(messageDataStr)
}

func (a *App) setCurrentConfig() {
	viper.Set(configDefaultUserKey, a.defaultUser)
	viper.Set(configUsersKey, a.users)
	viper.Set(configExportPathKey, a.FLoader.FilePrefix)
	err := viper.SafeWriteConfig()
	if err != nil {
		log.Println(err)
		err = viper.WriteConfig()
		if err != nil {
			log.Println(err)
		}
	}
}

type userList struct {
	Users []string `json:"Users"`
}

func (a *App) GetWeChatUserList() string {

	l := userList{}
	l.Users = a.users

	usersStr, _ := json.Marshal(l)
	str := string(usersStr)
	log.Println("users:", str)
	return str
}

func (a *App) OpenFileOrExplorer(filePath string, explorer bool) string {
	// if root, err := os.Getwd(); err == nil {
	// 	filePath = root + filePath[1:]
	// }
	// log.Println("OpenFileOrExplorer:", filePath)

	path := a.FLoader.FilePrefix + filePath
	err := utils.OpenFileOrExplorer(path, explorer)
	if err != nil {
		return "{\"result\": \"OpenFileOrExplorer failed\", \"status\":\"failed\"}"
	}

	return fmt.Sprintf("{\"result\": \"%s\", \"status\":\"OK\"}", "")
}

func (a *App) GetWeChatRoomUserList(roomId string) string {
	userlist, err := a.provider.WeChatGetChatRoomUserList(roomId)
	if err != nil {
		log.Println("WeChatGetChatRoomUserList:", err)
		return ""
	}

	userListStr, _ := json.Marshal(userlist)

	return string(userListStr)
}

func (a *App) GetAppVersion() string {
	return appVersion
}

func (a *App) GetAppIsFirstStart() bool {
	defer func() { a.firstStart = false }()
	return a.firstStart
}

func (a *App) GetWechatLocalAccountInfo() string {
	infos := WeChatAccountInfos{}
	infos.Info = make([]wechat.WeChatAccountInfo, 0)
	infos.Total = 0
	infos.CurrentAccount = a.defaultUser
	for i := range a.users {
		resPath := a.FLoader.FilePrefix + "\\User\\" + a.users[i]
		if _, err := os.Stat(resPath); err != nil {
			log.Println("GetWechatLocalAccountInfo:", resPath, err)
			continue
		}

		prefixResPath := "\\User\\" + a.users[i]
		info, err := wechat.WechatGetAccountInfo(resPath, prefixResPath, a.users[i])
		if err != nil {
			log.Println("GetWechatLocalAccountInfo", err)
			continue
		}

		infos.Info = append(infos.Info, *info)
		infos.Total += 1
	}

	infoString, _ := json.Marshal(infos)
	log.Println(string(infoString))

	return string(infoString)
}

func (a *App) WechatSwitchAccount(account string) bool {
	for i := range a.users {
		if a.users[i] == account {
			if a.provider != nil {
				a.provider.WechatWechatDataProviderClose()
				a.provider = nil
			}
			a.defaultUser = account
			a.setCurrentConfig()
			return true
		}
	}

	return false
}

func (a *App) GetExportPathStat() string {
	path := a.FLoader.FilePrefix
	log.Println("utils.GetPathStat ++")
	stat, err := utils.GetPathStat(path)
	log.Println("utils.GetPathStat --")
	if err != nil {
		log.Println("GetPathStat error:", path, err)
		var msg ErrorMessage
		msg.ErrorStr = fmt.Sprintf("%s:%v", path, err)
		msgStr, _ := json.Marshal(msg)
		return string(msgStr)
	}

	statString, _ := json.Marshal(stat)

	return string(statString)
}

func (a *App) ExportPathIsCanWrite() bool {
	path := a.FLoader.FilePrefix
	return utils.PathIsCanWriteFile(path)
}

func (a *App) OpenExportPath() {
	path := a.FLoader.FilePrefix
	runtime.BrowserOpenURL(a.ctx, path)
}

func (a *App) OpenDirectoryDialog() string {
	dialogOptions := runtime.OpenDialogOptions{
		Title: "选择导出路径",
	}
	selectedDir, err := runtime.OpenDirectoryDialog(a.ctx, dialogOptions)
	if err != nil {
		log.Println("OpenDirectoryDialog:", err)
		return ""
	}

	if selectedDir == "" {
		log.Println("Cancel selectedDir")
		return ""
	}

	if selectedDir == a.FLoader.FilePrefix {
		log.Println("same path No need SetFilePrefix")
		return ""
	}

	if !utils.PathIsCanWriteFile(selectedDir) {
		log.Println("PathIsCanWriteFile:", selectedDir, "error")
		return ""
	}

	a.FLoader.SetFilePrefix(selectedDir)
	log.Println("OpenDirectoryDialog:", selectedDir)
	a.scanAccountByPath(selectedDir)
	return selectedDir
}

func (a *App) scanAccountByPath(path string) error {
	infos := WeChatAccountInfos{}
	infos.Info = make([]wechat.WeChatAccountInfo, 0)
	infos.Total = 0
	infos.CurrentAccount = ""

	userPath := path + "\\User\\"
	if _, err := os.Stat(userPath); err != nil {
		return err
	}

	dirs, err := os.ReadDir(userPath)
	if err != nil {
		log.Println("ReadDir", err)
		return err
	}

	for i := range dirs {
		if !dirs[i].Type().IsDir() {
			continue
		}
		log.Println("dirs[i].Name():", dirs[i].Name())
		resPath := path + "\\User\\" + dirs[i].Name()
		prefixResPath := "\\User\\" + dirs[i].Name()
		info, err := wechat.WechatGetAccountInfo(resPath, prefixResPath, dirs[i].Name())
		if err != nil {
			log.Println("GetWechatLocalAccountInfo", err)
			continue
		}

		infos.Info = append(infos.Info, *info)
		infos.Total += 1
	}

	users := make([]string, 0)
	for i := 0; i < infos.Total; i++ {
		users = append(users, infos.Info[i].AccountName)
	}

	a.users = users
	found := false
	for i := range a.users {
		if a.defaultUser == a.users[i] {
			found = true
		}
	}

	if !found {
		a.defaultUser = ""
	}
	if a.defaultUser == "" && len(a.users) > 0 {
		a.defaultUser = a.users[0]
	}

	if len(a.users) > 0 {
		a.setCurrentConfig()
	}

	return nil
}

func (a *App) OepnLogFileExplorer() {
	utils.OpenFileOrExplorer(".\\app.log", true)
}

func (a *App) SaveFileDialog(file string, alisa string) string {
	filePath := a.FLoader.FilePrefix + file
	if _, err := os.Stat(filePath); err != nil {
		log.Println("SaveFileDialog:", err)
		return err.Error()
	}

	savePath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		DefaultFilename: alisa,
		Title:           "选择保存路径",
	})
	if err != nil {
		log.Println("SaveFileDialog:", err)
		return err.Error()
	}

	if savePath == "" {
		return ""
	}

	dirPath := filepath.Dir(savePath)
	if !utils.PathIsCanWriteFile(dirPath) {
		errStr := "Path Is Can't Write File: " + filepath.Dir(savePath)
		log.Println(errStr)
		return errStr
	}

	_, err = utils.CopyFile(filePath, savePath)
	if err != nil {
		log.Println("Error CopyFile", filePath, savePath, err)
		return err.Error()
	}

	return ""
}

func (a *App) GetSessionLastTime(userName string) string {
	if a.provider == nil || userName == "" {
		lastTime := &wechat.WeChatLastTime{}
		lastTimeString, _ := json.Marshal(lastTime)
		return string(lastTimeString)
	}

	lastTime := a.provider.WeChatGetSessionLastTime(userName)

	lastTimeString, _ := json.Marshal(lastTime)

	return string(lastTimeString)
}

func (a *App) SetSessionLastTime(userName string, stamp int64, messageId string) string {
	if a.provider == nil {
		return ""
	}

	lastTime := &wechat.WeChatLastTime{
		UserName:  userName,
		Timestamp: stamp,
		MessageId: messageId,
	}
	err := a.provider.WeChatSetSessionLastTime(lastTime)
	if err != nil {
		log.Println("WeChatSetSessionLastTime failed:", err.Error())
		return err.Error()
	}

	return ""
}

func (a *App) SetSessionBookMask(userName, tag, info string) string {
	if a.provider == nil || userName == "" {
		return "invaild params"
	}
	err := a.provider.WeChatSetSessionBookMask(userName, tag, info)
	if err != nil {
		log.Println("WeChatSetSessionBookMask failed:", err.Error())
		return err.Error()
	}

	return ""
}

func (a *App) DelSessionBookMask(markId string) string {
	if a.provider == nil || markId == "" {
		return "invaild params"
	}

	err := a.provider.WeChatDelSessionBookMask(markId)
	if err != nil {
		log.Println("WeChatDelSessionBookMask failed:", err.Error())
		return err.Error()
	}

	return ""
}

func (a *App) GetSessionBookMaskList(userName string) string {
	if a.provider == nil || userName == "" {
		return "invaild params"
	}
	markLIst, err := a.provider.WeChatGetSessionBookMaskList(userName)
	if err != nil {
		log.Println("WeChatGetSessionBookMaskList failed:", err.Error())
		_list := &wechat.WeChatBookMarkList{}
		_listString, _ := json.Marshal(_list)
		return string(_listString)
	}

	markLIstString, _ := json.Marshal(markLIst)
	return string(markLIstString)
}

func (a *App) SelectedDirDialog(title string) string {
	dialogOptions := runtime.OpenDialogOptions{
		Title: title,
	}
	selectedDir, err := runtime.OpenDirectoryDialog(a.ctx, dialogOptions)
	if err != nil {
		log.Println("OpenDirectoryDialog:", err)
		return ""
	}

	if selectedDir == "" {
		return ""
	}

	return selectedDir
}

func (a *App) ExportWeChatDataByUserName(userName, path string) string {
	if a.provider == nil || userName == "" || path == "" {
		return "invaild params" + userName
	}

	if !utils.PathIsCanWriteFile(path) {
		log.Println("PathIsCanWriteFile: " + path)
		return "PathIsCanWriteFile: " + path
	}

	exPath := path + "\\" + "wechatDataBackup_" + userName
	if _, err := os.Stat(exPath); err != nil {
		os.MkdirAll(exPath, os.ModePerm)
	} else {
		return "path exist:" + exPath
	}

	log.Println("ExportWeChatDataByUserName:", userName, exPath)
	err := a.provider.WeChatExportDataByUserName(userName, exPath)
	if err != nil {
		log.Println("WeChatExportDataByUserName failed:", err)
		return "WeChatExportDataByUserName failed:" + err.Error()
	}

	config := map[string]interface{}{
		"exportpath": ".\\",
		"userconfig": map[string]interface{}{
			"defaultuser": a.defaultUser,
			"users":       []string{a.defaultUser},
		},
	}

	configJson, err := json.MarshalIndent(config, "", "	")
	if err != nil {
		log.Println("MarshalIndent:", err)
		return "MarshalIndent:" + err.Error()
	}

	configPath := exPath + "\\" + "config.json"
	err = os.WriteFile(configPath, configJson, os.ModePerm)
	if err != nil {
		log.Println("WriteFile:", err)
		return "WriteFile:" + err.Error()
	}

	exeSrcPath, err := os.Executable()
	if err != nil {
		log.Println("Executable:", exeSrcPath)
		return "Executable:" + err.Error()
	}

	exeDstPath := exPath + "\\" + "wechatDataBackup.exe"
	log.Printf("Copy [%s] -> [%s]\n", exeSrcPath, exeDstPath)
	_, err = utils.CopyFile(exeSrcPath, exeDstPath)
	if err != nil {
		log.Println("CopyFile:", err)
		return "CopyFile:" + err.Error()
	}
	return ""

	return ""
}

func (a *App) GetAppIsShareData() bool {
	if a.provider != nil {
		return a.provider.IsShareData
	}
	return false
}

// 增量导出并备份新增数据
func (a *App) ExportWeChatDataWithIncrementalBackup(full bool, acountName string, enableBackup bool, backupPath string) {
	if a.provider != nil {
		a.provider.WechatWechatDataProviderClose()
		a.provider = nil
	}

	progress := make(chan string)
	go func() {
		var pInfo *wechat.WeChatInfo
		for i := range a.infoList.Info {
			if a.infoList.Info[i].AcountName == acountName {
				pInfo = &a.infoList.Info[i]
				break
			}
		}

		if pInfo == nil {
			close(progress)
			runtime.EventsEmit(a.ctx, "exportData", fmt.Sprintf("{\"status\":\"error\", \"result\":\"%s error\"}", acountName))
			return
		}

		prefixExportPath := a.FLoader.FilePrefix + "\\User\\"
		_, err := os.Stat(prefixExportPath)
		if err != nil {
			os.Mkdir(prefixExportPath, os.ModeDir)
		}

		expPath := prefixExportPath + pInfo.AcountName
		
		// 记录导出前的文件状态（用于检测新增数据）
		var backupResult *IncrementalBackupResult
		if enableBackup && !full {
			backupResult = a.scanExistingFiles(expPath, backupPath)
		}

		// 执行原有的增量导出逻辑
		_, err = os.Stat(expPath)
		if err == nil {
			if !full {
				os.RemoveAll(expPath + "\\Msg")
			} else {
				os.RemoveAll(expPath)
			}
		}

		_, err = os.Stat(expPath)
		if err != nil {
			os.Mkdir(expPath, os.ModeDir)
		}

		// 执行增量导出
		go wechat.ExportWeChatAllData(*pInfo, expPath, progress)

		// 监听导出进度
		for p := range progress {
			log.Println(p)
			runtime.EventsEmit(a.ctx, "exportData", p)
		}

		// 导出完成后，备份新增数据
		if enableBackup && !full && backupResult != nil {
			backupResult = a.backupNewData(expPath, backupResult)
			
			// 发送备份结果
			resultJson, _ := json.Marshal(backupResult)
			runtime.EventsEmit(a.ctx, "incrementalBackup", string(resultJson))
		}

		// 导出完成后，执行新消息导出
		log.Println("开始检查是否需要导出新消息，full=", full)
		runtime.EventsEmit(a.ctx, "exportData", "{\"status\":\"processing\", \"result\":\"开始导出新消息\", \"progress\": 95}")
		if !full {
			log.Println("执行新消息导出，账号名=", pInfo.AcountName, "导出路径=", expPath)
			newMessageResult := a.exportNewMessages(pInfo.AcountName, expPath)
			if newMessageResult != nil {
				log.Println("新消息导出完成，结果=", newMessageResult)
				// 发送新消息导出结果
				resultJson, _ := json.Marshal(newMessageResult)
				runtime.EventsEmit(a.ctx, "newMessageExport", string(resultJson))
			} else {
				log.Println("新消息导出返回nil结果")
			}
		} else {
			log.Println("跳过新消息导出，因为这是全量导出")
		}
		
		// 发送导出完成事件，通知前端刷新消息列表
		runtime.EventsEmit(a.ctx, "exportData", "{\"status\":\"completed\", \"result\":\"导出完成\", \"progress\": 100}")
		runtime.EventsEmit(a.ctx, "refreshMessageList", "{\"action\":\"refresh\"}")

		// 更新用户配置
		a.defaultUser = pInfo.AcountName
		hasUser := false
		for _, user := range a.users {
			if user == pInfo.AcountName {
				hasUser = true
				break
			}
		}
		if !hasUser {
			a.users = append(a.users, pInfo.AcountName)
		}
		a.setCurrentConfig()
	}()
}

// 扫描现有文件状态
func (a *App) scanExistingFiles(expPath, backupPath string) *IncrementalBackupResult {
	result := &IncrementalBackupResult{
		NewDataRecords: make([]NewDataRecord, 0),
		BackupPath:     backupPath,
	}

	// 创建备份目录
	backupDir := fmt.Sprintf("%s\\%s\\%d", backupPath, a.defaultUser, time.Now().Unix())
	os.MkdirAll(backupDir, os.ModePerm)
	result.BackupPath = backupDir

	// 扫描Msg目录（数据库文件）
	msgPath := expPath + "\\Msg"
	if _, err := os.Stat(msgPath); err == nil {
		a.scanDirectoryForBackup(msgPath, backupDir, "database", result)
	}

	// 扫描FileStorage目录（媒体文件）
	fileStoragePath := expPath + "\\FileStorage"
	if _, err := os.Stat(fileStoragePath); err == nil {
		a.scanDirectoryForBackup(fileStoragePath, backupDir, "media", result)
	}

	return result
}

// 扫描目录并记录文件信息
func (a *App) scanDirectoryForBackup(srcPath, backupDir, dataType string, result *IncrementalBackupResult) {
	err := filepath.Walk(srcPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !info.IsDir() {
			record := NewDataRecord{
				FilePath:   path,
				FileSize:   info.Size(),
				ModifyTime: info.ModTime().Unix(),
				DataType:   dataType,
			}
			
			// 计算文件哈希
			if hash, err := utils.CalculateFileHash(path); err == nil {
				record.FileHash = hash
			}
			
			result.NewDataRecords = append(result.NewDataRecords, record)
			result.TotalFiles++
		}
		return nil
	})

	if err != nil {
		log.Printf("Error scanning directory %s: %v", srcPath, err)
	}
}

// 备份新增数据
func (a *App) backupNewData(expPath string, backupResult *IncrementalBackupResult) *IncrementalBackupResult {
	log.Println("Starting incremental backup...")
	
	for i := range backupResult.NewDataRecords {
		record := &backupResult.NewDataRecords[i]
		
		// 检查文件是否为新文件或已修改
		if info, err := os.Stat(record.FilePath); err == nil {
			// 检查文件是否已存在且未修改
			existingRecord := a.findExistingRecord(record.FilePath)
			if existingRecord != nil && 
			   existingRecord.FileHash == record.FileHash && 
			   existingRecord.FileSize == record.FileSize {
				continue // 文件未变化，跳过备份
			}
			
			// 更新文件信息
			record.FileSize = info.Size()
			record.ModifyTime = info.ModTime().Unix()
			
			// 计算相对路径
			relPath, err := filepath.Rel(expPath, record.FilePath)
			if err != nil {
				log.Printf("Error calculating relative path: %v", err)
				continue
			}
			
			// 确定备份目标路径
			backupFilePath := filepath.Join(backupResult.BackupPath, relPath)
			backupDir := filepath.Dir(backupFilePath)
			
			// 创建备份目录
			if err := os.MkdirAll(backupDir, os.ModePerm); err != nil {
				log.Printf("Error creating backup directory: %v", err)
				continue
			}
			
			// 复制文件到备份目录
			if _, err := utils.CopyFile(record.FilePath, backupFilePath); err == nil {
				record.BackupPath = backupFilePath
				backupResult.BackupFiles++
				backupResult.BackupSize += record.FileSize
				log.Printf("Backed up: %s -> %s", record.FilePath, backupFilePath)
			} else {
				log.Printf("Error backing up file %s: %v", record.FilePath, err)
			}
		}
	}
	
	backupResult.NewFiles = backupResult.BackupFiles
	log.Printf("Incremental backup completed: %d files backed up, %d bytes", 
		backupResult.BackupFiles, backupResult.BackupSize)
	
	return backupResult
}

// 查找现有记录
func (a *App) findExistingRecord(filePath string) *NewDataRecord {
	// 这里可以从配置文件或数据库中查找现有记录
	// 简化实现：从配置文件中读取
	configPath := fmt.Sprintf("%s\\backup_history.json", a.FLoader.FilePrefix)
	if data, err := os.ReadFile(configPath); err == nil {
		var records []NewDataRecord
		if err := json.Unmarshal(data, &records); err == nil {
			for i := range records {
				if records[i].FilePath == filePath {
					return &records[i]
				}
			}
		}
	}
	return nil
}

// 设置增量备份配置
func (a *App) SetIncrementalBackupConfig(config IncrementalBackupConfig) bool {
	configPath := fmt.Sprintf("%s\\incremental_backup_config.json", a.FLoader.FilePrefix)
	configJson, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Printf("Error marshaling backup config: %v", err)
		return false
	}
	
	if err := os.WriteFile(configPath, configJson, os.ModePerm); err != nil {
		log.Printf("Error writing backup config: %v", err)
		return false
	}
	
	return true
}

// 获取增量备份配置
func (a *App) GetIncrementalBackupConfig() string {
	configPath := fmt.Sprintf("%s\\incremental_backup_config.json", a.FLoader.FilePrefix)
	if data, err := os.ReadFile(configPath); err == nil {
		return string(data)
	}
	
	// 返回默认配置
	defaultConfig := IncrementalBackupConfig{
		EnableBackup:      false,
		BackupPath:        "",
		LastBackupTime:    0,
		MaxBackupVersions: 10,
	}
	configJson, _ := json.MarshalIndent(defaultConfig, "", "  ")
	return string(configJson)
}

// 导出新消息（2025年10月16日之后的消息）
func (a *App) exportNewMessages(accountName, expPath string) *NewMessageExportResult {
	log.Println("Starting new message export...")
	log.Println("账号名:", accountName, "导出路径:", expPath)
	
	// 设置开始时间：2025年10月16日 00:00:00
	startTime := time.Date(2025, 10, 16, 0, 0, 0, 0, time.Local).Unix()
	
	// 创建保存目录
	saveTime := time.Now().Format("2006-01-02_15-04-05")
	savePath := fmt.Sprintf(".\\save\\%s", saveTime)
	log.Println("保存路径:", savePath)
	if err := os.MkdirAll(savePath, os.ModePerm); err != nil {
		log.Printf("Error creating save directory: %v", err)
		return nil
	}
	
	result := &NewMessageExportResult{
		SavePath:   savePath,
		ExportTime: saveTime,
		Contacts:   make([]ContactMessageData, 0),
	}
	
	// 初始化数据提供者
	if a.provider == nil {
		log.Println("创建新的数据提供者...")
		var err error
		a.provider, err = wechat.CreateWechatDataProvider(expPath, "")
		if err != nil {
			log.Printf("Error creating data provider: %v", err)
			return nil
		}
		defer a.provider.WechatWechatDataProviderClose()
		log.Println("数据提供者创建成功")
	} else {
		log.Println("使用现有数据提供者")
	}
	
	// 获取所有联系人
	log.Println("获取联系人列表...")
	contactList, err := a.provider.WeChatGetContactList(0, 1000)
	if err != nil {
		log.Printf("Error getting contact list: %v", err)
		return nil
	}
	log.Println("联系人列表获取成功，共", len(contactList.Users), "个联系人")
	
	log.Printf("Found %d contacts, processing new messages since %s", 
		len(contactList.Users), time.Unix(startTime, 0).Format("2006-01-02 15:04:05"))
	
	// 处理每个联系人的新消息
	for _, contact := range contactList.Users {
		contactData := a.processContactNewMessages(contact, startTime, savePath)
		if contactData != nil && contactData.MessageCount > 0 {
			result.Contacts = append(result.Contacts, *contactData)
			result.TotalMessages += contactData.MessageCount
		}
	}
	
	result.TotalContacts = len(result.Contacts)
	log.Printf("New message export completed: %d contacts, %d total messages", 
		result.TotalContacts, result.TotalMessages)
	
	return result
}

// 处理单个联系人的新消息
func (a *App) processContactNewMessages(contact wechat.WeChatUserInfo, startTime int64, savePath string) *ContactMessageData {
	// 获取该联系人的新消息 - 使用Backward方向获取大于startTime的消息
	messages, err := a.provider.WeChatGetMessageListByTime(
		contact.UserName, 
		startTime, 
		1000, // 每次获取1000条消息
		wechat.Message_Search_Backward, // 改为Backward以获取大于startTime的消息
	)
	
	if err != nil {
		log.Printf("Error getting messages for %s: %v", contact.NickName, err)
		return nil
	}
	
	if messages.Total == 0 {
		return nil
	}
	
	// 构建对话数据
	dialogueGroup := DialogueGroup{
		Instruction: fmt.Sprintf("%s 的新消息对话", contact.NickName),
		Dialogue:    make([]DialogueMessage, 0),
	}
	
	// 处理每条消息 - 按时间顺序排列，最新的消息在最后
	for _, msg := range messages.Rows {
		// 跳过系统消息
		if msg.Type == wechat.Wechat_Message_Type_System {
			continue
		}
		
		// 确保只处理2025-10-16之后的消息
		if msg.CreateTime < startTime {
			log.Printf("跳过旧消息: %s, 时间: %s, 开始时间: %s", 
				contact.NickName, 
				time.Unix(msg.CreateTime, 0).Format("2006-01-02 15:04:05"),
				time.Unix(startTime, 0).Format("2006-01-02 15:04:05"))
			continue
		}
		
		// 确定发言人
		var speaker string
		if msg.IsSender == 1 {
			// 自己发送的消息
			speaker = a.provider.SelfInfo.NickName
		} else {
			// 别人发送的消息
			if contact.IsGroup {
				// 群聊消息，从UserInfo.UserName获取具体说话人信息
				if msg.UserInfo.UserName != "" {
					// 尝试从用户信息缓存中获取昵称
					if userInfo, err := a.provider.WechatGetUserInfoByNameOnCache(msg.UserInfo.UserName); err == nil {
						speaker = userInfo.NickName // 使用原始昵称，不使用备注
					} else {
						// 如果获取不到用户信息，使用UserInfo中的信息
						if msg.UserInfo.NickName != "" {
							speaker = msg.UserInfo.NickName
						} else {
							speaker = msg.UserInfo.UserName // 兜底使用用户名
						}
					}
				} else {
					speaker = contact.NickName // 兜底使用群聊名
				}
			} else {
				// 私聊消息，使用原始昵称而非备注
				speaker = contact.NickName // 使用原始昵称，不使用备注
			}
		}
		
		// 处理消息内容
		text := a.processMessageContent(&msg, savePath)
		if text == "" {
			continue
		}
		
		// 格式化时间
		msgTime := time.Unix(msg.CreateTime, 0).Format("2006-01-02 15:04:05")
		
		// 调试日志：记录说话人识别信息
		textPreview := text
		if len(text) > 20 {
			textPreview = text[:20]
		}
		if contact.IsGroup {
			log.Printf("群聊消息 - 群名: %s, Talker: %s, UserInfo.UserName: %s, UserInfo.NickName: %s, 识别出的说话人: %s, 内容: %s", 
				contact.NickName, msg.Talker, msg.UserInfo.UserName, msg.UserInfo.NickName, speaker, textPreview)
		} else {
			log.Printf("私聊消息 - 联系人: %s, 识别出的说话人: %s, 内容: %s", 
				contact.NickName, speaker, textPreview)
		}
		
		dialogueMessage := DialogueMessage{
			Index:   len(dialogueGroup.Dialogue) + 1, // 使用当前对话长度+1作为index
			Speaker: speaker,
			Text:    text,
			Time:    msgTime,
		}
		
		dialogueGroup.Dialogue = append(dialogueGroup.Dialogue, dialogueMessage)
	}
	
	if len(dialogueGroup.Dialogue) == 0 {
		return nil
	}
	
	// 创建联系人数据
	contactData := &ContactMessageData{
		ContactName: contact.NickName,
		MessageCount: len(dialogueGroup.Dialogue),
		FilePath:    fmt.Sprintf("%s\\%s.json", savePath, a.sanitizeFileName(contact.NickName)),
		Dialogue:    []DialogueGroup{dialogueGroup},
	}
	
	// 保存到JSON文件
	if err := a.saveContactMessagesToJSON(contactData); err != nil {
		log.Printf("Error saving messages for %s: %v", contact.NickName, err)
		return nil
	}
	
	log.Printf("Exported %d messages for %s", contactData.MessageCount, contact.NickName)
	return contactData
}

// 处理消息内容（包括媒体文件）
func (a *App) processMessageContent(msg *wechat.WeChatMessage, savePath string) string {
	switch msg.Type {
	case wechat.Wechat_Message_Type_Text:
		return msg.Content
		
	case wechat.Wechat_Message_Type_Picture:
		if msg.ImagePath != "" {
			// 构建正确的图片路径
			imagePath := a.buildCorrectMediaPath(msg.ImagePath, "Image")
			if imagePath != "" && a.fileExists(imagePath) {
				return fmt.Sprintf("[图片] %s", imagePath)
			}
		}
		return "[图片] 文件不存在"
		
	case wechat.Wechat_Message_Type_Video:
		if msg.VideoPath != "" {
			// 构建正确的视频路径
			videoPath := a.buildCorrectMediaPath(msg.VideoPath, "Video")
			if videoPath != "" && a.fileExists(videoPath) {
				return fmt.Sprintf("[视频] %s", videoPath)
			}
		}
		return "[视频] 文件不存在"
		
	case wechat.Wechat_Message_Type_Voice:
		if msg.VoicePath != "" {
			// 构建正确的语音路径
			voicePath := a.buildCorrectMediaPath(msg.VoicePath, "Voice")
			if voicePath != "" && a.fileExists(voicePath) {
				return fmt.Sprintf("[语音] %s", voicePath)
			}
		}
		return "[语音] 文件不存在"
		
	case wechat.Wechat_Message_Type_Location:
		if msg.LocationInfo.Label != "" {
			return fmt.Sprintf("[位置] %s", msg.LocationInfo.Label)
		}
		return "[位置]"
		
	case wechat.Wechat_Message_Type_Visit_Card:
		if msg.VisitInfo.NickName != "" {
			return fmt.Sprintf("[名片] %s", msg.VisitInfo.NickName)
		}
		return "[名片]"
		
	case wechat.Wechat_Message_Type_Misc:
		return a.processMiscMessage(msg, savePath)
		
	default:
		return fmt.Sprintf("[其他消息类型: %d]", msg.Type)
	}
}

// 获取杂项消息类型描述
func (a *App) getMiscMessageDescription(subType int) string {
	switch subType {
	case wechat.Wechat_Misc_Message_TEXT:
		return "文本消息"
	case wechat.Wechat_Misc_Message_Music:
		return "音乐消息"
	case wechat.Wechat_Misc_Message_ThirdVideo:
		return "第三方视频"
	case wechat.Wechat_Misc_Message_CardLink:
		return "链接卡片"
	case wechat.Wechat_Misc_Message_File:
		return "文件消息"
	case wechat.Wechat_Misc_Message_CustomEmoji:
		return "自定义表情"
	case wechat.Wechat_Misc_Message_ShareEmoji:
		return "分享表情"
	case wechat.Wechat_Misc_Message_ForwardMessage:
		return "聊天记录合集"
	case wechat.Wechat_Misc_Message_Applet:
		return "小程序"
	case wechat.Wechat_Misc_Message_Applet2:
		return "小程序2"
	case wechat.Wechat_Misc_Message_Channels:
		return "视频号"
	case wechat.Wechat_Misc_Message_Refer:
		return "引用消息"
	case wechat.Wechat_Misc_Message_Live:
		return "直播"
	case wechat.Wechat_Misc_Message_Game:
		return "游戏消息"
	case wechat.Wechat_Misc_Message_Notice:
		return "通知消息"
	case wechat.Wechat_Misc_Message_Live2:
		return "直播2"
	case wechat.Wechat_Misc_Message_TingListen:
		return "听歌识曲"
	case wechat.Wechat_Misc_Message_Transfer:
		return "转账消息"
	case wechat.Wechat_Misc_Message_RedPacket:
		return "红包消息"
	default:
		return fmt.Sprintf("未知杂项消息(%d)", subType)
	}
}

// 处理杂项消息
func (a *App) processMiscMessage(msg *wechat.WeChatMessage, savePath string) string {
	switch msg.SubType {
	case wechat.Wechat_Misc_Message_File:
		if msg.FileInfo.FileName != "" {
			// 构建正确的文件路径
			filePath := a.buildCorrectMediaPath(msg.FileInfo.FilePath, "File")
			if filePath != "" && a.fileExists(filePath) {
				return fmt.Sprintf("[文件] %s", filePath)
			}
			return fmt.Sprintf("[文件] %s (文件不存在)", msg.FileInfo.FileName)
		}
		return "[文件]"
		
	case wechat.Wechat_Misc_Message_Music:
		if msg.MusicInfo.Title != "" {
			return fmt.Sprintf("[音乐] %s - %s", msg.MusicInfo.Title, msg.MusicInfo.DisPlayName)
		}
		return "[音乐]"
		
	case wechat.Wechat_Misc_Message_ThirdVideo:
		if msg.ThumbPath != "" {
			thumbPath := a.buildCorrectMediaPath(msg.ThumbPath, "Thumb")
			if thumbPath != "" {
				return fmt.Sprintf("[第三方视频] %s", thumbPath)
			}
		}
		return "[第三方视频]"
		
	case wechat.Wechat_Misc_Message_CardLink:
		if msg.ThumbPath != "" {
			thumbPath := a.buildCorrectMediaPath(msg.ThumbPath, "Thumb")
			if thumbPath != "" {
				return fmt.Sprintf("[链接卡片] %s", thumbPath)
			}
		}
		return "[链接卡片]"
		
	case wechat.Wechat_Misc_Message_Applet, wechat.Wechat_Misc_Message_Applet2:
		if msg.ThumbPath != "" {
			thumbPath := a.buildCorrectMediaPath(msg.ThumbPath, "Thumb")
			if thumbPath != "" {
				return fmt.Sprintf("[小程序] %s", thumbPath)
			}
		}
		return "[小程序]"
		
	case wechat.Wechat_Misc_Message_Channels:
		if msg.ThumbPath != "" {
			thumbPath := a.buildCorrectMediaPath(msg.ThumbPath, "Thumb")
			if thumbPath != "" {
				return fmt.Sprintf("[视频号] %s", thumbPath)
			}
		}
		return "[视频号]"
		
	default:
		return fmt.Sprintf("[%s]", a.getMiscMessageDescription(msg.SubType))
	}
}

// 构建正确的媒体文件路径
func (a *App) buildCorrectMediaPath(originalPath, mediaType string) string {
	if originalPath == "" {
		return ""
	}
	
	// 获取用户数据目录
	userDataDir := a.FLoader.FilePrefix + "\\User\\" + a.defaultUser
	
	// 根据媒体类型构建路径
	var correctPath string
	
	// 处理路径分隔符，确保使用反斜杠
	normalizedPath := strings.ReplaceAll(originalPath, "/", "\\")
	
	// 调试日志：记录原始路径信息
	log.Printf("媒体文件路径构建开始 - 原始路径: %s, 媒体类型: %s, 用户名: %s", 
		originalPath, mediaType, a.provider.SelfInfo.UserName)
	
	// 检查路径是否已经包含完整路径
	if strings.Contains(normalizedPath, "FileStorage\\") {
		// 路径已经包含FileStorage，直接拼接用户数据目录
		if strings.HasPrefix(normalizedPath, "\\") {
			correctPath = userDataDir + normalizedPath
		} else {
			correctPath = userDataDir + "\\" + normalizedPath
		}
	} else {
		// 路径不包含FileStorage，需要根据媒体类型添加正确的子目录
		// 确保路径以反斜杠开头
		if !strings.HasPrefix(normalizedPath, "\\") {
			normalizedPath = "\\" + normalizedPath
		}
		
		switch mediaType {
		case "Image":
			// 图片文件路径：FileStorage/Image/
			correctPath = userDataDir + "\\FileStorage\\Image" + normalizedPath
		case "Thumb":
			// 缩略图路径：FileStorage/MsgAttach/xxx/Thumb/
			correctPath = userDataDir + "\\FileStorage\\MsgAttach" + normalizedPath
		case "Video":
			// 视频文件路径：FileStorage/Video/
			correctPath = userDataDir + "\\FileStorage\\Video" + normalizedPath
		case "Voice":
			// 语音文件路径：FileStorage/Voice/
			correctPath = userDataDir + "\\FileStorage\\Voice" + normalizedPath
		case "File":
			// 文件路径：FileStorage/File/
			correctPath = userDataDir + "\\FileStorage\\File" + normalizedPath
		default:
			// 默认路径：FileStorage/
			correctPath = userDataDir + "\\FileStorage" + normalizedPath
		}
	}
	
	// 清理路径中的双反斜杠
	correctPath = strings.ReplaceAll(correctPath, "\\\\", "\\")
	
	// 调试日志
	log.Printf("媒体文件路径构建完成 - 构建路径: %s, 文件存在: %v", 
		correctPath, a.fileExists(correctPath))
	
	return correctPath
}

// 检查文件是否存在
func (a *App) fileExists(filePath string) bool {
	_, err := os.Stat(filePath)
	return err == nil
}

// 保存联系人消息到JSON文件
func (a *App) saveContactMessagesToJSON(contactData *ContactMessageData) error {
	// 确保目录存在
	dir := filepath.Dir(contactData.FilePath)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return err
	}
	
	// 序列化为JSON
	jsonData, err := json.MarshalIndent(contactData.Dialogue, "", "  ")
	if err != nil {
		return err
	}
	
	// 写入文件
	return os.WriteFile(contactData.FilePath, jsonData, os.ModePerm)
}

// 清理文件名中的非法字符
func (a *App) sanitizeFileName(fileName string) string {
	// 替换Windows文件名中的非法字符
	invalidChars := []string{"\\", "/", ":", "*", "?", "\"", "<", ">", "|"}
	result := fileName
	
	for _, char := range invalidChars {
		result = strings.ReplaceAll(result, char, "_")
	}
	
	// 限制文件名长度
	if len(result) > 100 {
		result = result[:100]
	}
	
	return result
}

// 设置新消息导出配置
func (a *App) SetNewMessageExportConfig(config NewMessageExportConfig) bool {
	configPath := fmt.Sprintf("%s\\new_message_export_config.json", a.FLoader.FilePrefix)
	configJson, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Printf("Error marshaling new message export config: %v", err)
		return false
	}
	
	if err := os.WriteFile(configPath, configJson, os.ModePerm); err != nil {
		log.Printf("Error writing new message export config: %v", err)
		return false
	}
	
	return true
}

// 获取新消息导出配置
func (a *App) GetNewMessageExportConfig() string {
	configPath := fmt.Sprintf("%s\\new_message_export_config.json", a.FLoader.FilePrefix)
	if data, err := os.ReadFile(configPath); err == nil {
		return string(data)
	}
	
	// 返回默认配置
	defaultConfig := NewMessageExportConfig{
		EnableExport:   true,
		StartTime:      time.Date(2025, 10, 16, 0, 0, 0, 0, time.Local).Unix(),
		SavePath:       ".\\save",
		IncludeMedia:   true,
		GroupByContact: true,
	}
	configJson, _ := json.MarshalIndent(defaultConfig, "", "  ")
	return string(configJson)
}

// 测试新消息导出功能
func (a *App) TestNewMessageExport(accountName string) string {
	log.Println("测试新消息导出功能...")
	
	// 设置导出路径
	expPath := a.FLoader.FilePrefix + "\\User\\" + accountName
	log.Println("测试导出路径:", expPath)
	
	// 测试各种路径格式
	testCases := []struct {
		path      string
		mediaType string
		desc      string
	}{
		{"test/path/file.jpg", "Image", "简单图片路径"},
		{"test/path/document.docx", "File", "简单文件路径"},
		{"FileStorage/File/2025-10/2.下降沿实例说明(11).JSON", "File", "包含FileStorage的文件路径"},
		{"\\FileStorage\\Image\\2025-10\\test.jpg", "Image", "以反斜杠开头的图片路径"},
		{"FileStorage\\Video\\2025-10\\test.mp4", "Video", "包含FileStorage的视频路径"},
		{"MsgAttach\\abc123\\Image\\test.jpg", "Image", "MsgAttach图片路径"},
		{"MsgAttach\\abc123\\Thumb\\test.jpg", "Thumb", "MsgAttach缩略图路径"},
	}
	
	for _, tc := range testCases {
		resultPath := a.buildCorrectMediaPath(tc.path, tc.mediaType)
		log.Printf("测试 %s: %s -> %s", tc.desc, tc.path, resultPath)
	}
	
	// 执行新消息导出
	result := a.exportNewMessages(accountName, expPath)
	if result != nil {
		resultJson, _ := json.Marshal(result)
		log.Println("测试结果:", string(resultJson))
		return string(resultJson)
	} else {
		log.Println("测试失败，返回nil")
		return "{\"error\": \"测试失败，返回nil\"}"
	}
}
