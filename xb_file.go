package main

import "context"

/*
type DOMFile struct {
	FileEntry struct {
		ID                string
		Name              string
		ParentID          string
		SubTreeDirNum     int
		SubTreeFileNum    int
		CreateTimeForSort int64
		Deleted           bool
		Dir               bool
		DirNum            int
		Tags              string
		UserID            string

		FullPath string
	}

	Children []*DOMFile
}

func (yf *DOMFile) IsUpdated(f *DOMFile) bool {
	return f.Size() != yf.Size() || f.ModTime().Unix() != yf.ModTime().Unix()
}

// 上传的笔记

func (yf *DOMFile) Dict() *zerolog.Event {
	return zerolog.Dict().Str("name", yf.Name()).Str("id", yf.ID()).
		Int64("size", yf.Size())
}

// fs.File
// 名字不是唯一的，以id为准
func (yf *DOMFile) ID() string {
	return yf.FileEntry.ID
}

func (yf *DOMFile) Name() string {
	return yf.FileEntry.Name
}

func (yf *DOMFile) Size() int64 {
	return 0
}

func (yf *DOMFile) ModTime() time.Time {
	return time.Unix(0, 0)
}
func (yf *DOMFile) IsDir() bool {
	return yf.FileEntry.Dir
}
*/

// 本地文件缓存信息，用于增量拉取
type YdFileSystem struct {
	// ydNoteSession

	domCtx context.Context
	// files  map[string]*DOMFile // 所有文件，包含目录
	// cacheFiles map[string]*DOMFile
}

func (yfs *YdFileSystem) Init(ydContext *YDNoteContext) error {
	// yfs.files = make(map[string]*DOMFile)
	// yfs.cacheFiles = yfs.loadCache()

	doYoudaoNoteLogin(ydContext, entryURL, nil)
	return nil
}

/*
func (yfs *YdFileSystem) UpdateFile(f *DOMFile) {
	yfs.files[f.ID()] = f
}

func getFileLocalPath(files map[string]*DOMFile, f *DOMFile) string {
	tmp := make([]string, 0, 10)
	pf := f
	var ok bool
	for {
		// 共享给我的文档
		if pf.FileEntry.ParentID == "-2" {
			tmp = append(tmp, "_sharein_")
		}
		if pf, ok = files[pf.FileEntry.ParentID]; ok {
			tmp = append(tmp, pf.Name())
		} else {
			break
		}
	}
	if len(tmp) >= 2 {
		for i := 0; i < len(tmp)/2; i++ {
			tmp[i], tmp[len(tmp)-i-1] = tmp[len(tmp)-i-1], tmp[i]
		}
	}
	tmp = append(tmp, f.Name())

	return localFileDir(tmp...)
}

func (yfs *YdFileSystem) startPull(ydContext *YDNoteContext) {
	defer ydContext.ContextCancel()

	// 无论如何拉取远程根目录信息
	begin := time.Now()
	// log.Info().Msg("start pull remote file info")
	// yfs.walkRemoteFile(ydContext, "/", "")         // 拉取我的笔记列表
	// yfs.walkRemoteFile(ydContext, "_myshare_", "") // 拉取与我分享的笔记列表
	// log.Info().Dur("cost", time.Since(begin)).Int("file_count", len(yfs.files)).Msg("pull remote file info finish")

	// // 对比本地缓存，确定删除、更新、还是增加
	// begin = time.Now()
	// log.Info().Msg("start download remote file")
	// cache := yfs.loadCache()
	// err := doDeltaPull(ydContext, cache, yfs.files)
	log.Info().Dur("cost", time.Since(begin)).Int("file_count", len(yfs.files)).Msg("download remote file finish")
}

// 加载本地缓存文件
// func (yfs *YdFileSystem) loadCache() map[string]*DOMFile {
// 	cf := localCacheDir(localFileInfo)
// 	if _, inErr := os.Stat(cf); os.IsNotExist(inErr) {
// 		return nil
// 	}
// 	data, err := os.ReadFile(cf)
// 	if err != nil {
// 		log.Error().Err(err).Msg("skip local cache file info")
// 		return nil
// 	}

// 	files := make(map[string]*DOMFile)
// 	err = json.Unmarshal(data, &files)
// 	if err != nil {
// 		log.Error().Err(err).Msg("skip local cache file info")
// 		return nil
// 	}
// 	log.Info().Int("file_count", len(files)).Msg("load cache file info")
// 	return files
// }

func (yfs *YdFileSystem) Open(name string) (fs.File, error) {
	// if f, ok := yfs.files[name]; ok {
	// 	return f, nil
	// }
	return nil, fmt.Errorf("file not found:%s", name)
}

func (yfs *YdFileSystem) ReadDir(name string) ([]fs.DirEntry, error) {
	var folderItems []*cdp.Node
	err := listChild("#bag-list-wrapper > div.src-FileList-packages-ListBody-styles-module__list-wrapper--3bkKg.src-FileList-packages-ListBody-styles-module__visible--1snrQ",
		&folderItems).Do(yfs.domCtx)
	if err != nil {
		return nil, err
	}

	return folderItems, nil
}

func (yf *DOMFile) Mode() fs.FileMode {
	if yf.IsDir() {
		return fs.ModeDir
	}
	return fs.ModeDevice
}

func (yf *DOMFile) Sys() interface{} {
	return nil
}

func (yf *DOMFile) Stat() (fs.FileInfo, error) {
	return yf, nil
}

func (yf *DOMFile) Read([]byte) (int, error) {
	return 0, nil
}
func (yf *DOMFile) Close() error {
	return nil
}

// fs.DirEntry
func (yf *DOMFile) Type() fs.FileMode {
	return yf.Mode()
}

func (yf *DOMFile) Info() (fs.FileInfo, error) {
	return yf.Stat()
}

// fs.ReadDir
func (yf *DOMFile) ReadDir(n int) ([]fs.DirEntry, error) {
	result := make([]fs.DirEntry, 0, 30)
	if n < 0 {
		n = len(yf.Children)
	}
	for i := 0; i < n; i++ {
		result = append(result, yf.Children[i])
	}
	return result, nil
}
*/
