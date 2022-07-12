package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"github.com/mdp/qrterminal/v3"
	"github.com/rs/zerolog/log"
)

var (
	signInfo      *signStruct
	signToken     string
	authorization string

	downloadFileRelativePath sync.Map // 下载文件的相对目录，文件下载结束之后移动到相对目录
	downloadingFiles         sync.Map
	downloadingCount         int32
	// downloadBucket   = make(chan bool, 2) // 可同时下载文件的个数
)

// 遍历缓存信息
type iterStructLevel struct {
	title     string
	nextIndex int // 下次要遍历的索引
}

type inteStruct struct {
	levelIndexs []iterStructLevel
}

type downloadFileCahce struct {
	relitavePath string
}

func (is *inteStruct) AppendLevel(title string, nextIndex int) {
	is.levelIndexs = append(is.levelIndexs, iterStructLevel{title: title, nextIndex: nextIndex})
}

func (is *inteStruct) getCurLevelIndex() *iterStructLevel {
	if len(is.levelIndexs) > 0 {
		return &is.levelIndexs[len(is.levelIndexs)-1]
	}
	return nil
}

func (is *inteStruct) popLevel() {
	if len(is.levelIndexs) > 0 {
		is.levelIndexs = is.levelIndexs[:len(is.levelIndexs)-1]
	}
}

func (is *inteStruct) cacheLevelCount() int {
	return len(is.levelIndexs)
}

func (is *inteStruct) joinPath() string {
	array := make([]string, 0, len(is.levelIndexs))
	for k, v := range is.levelIndexs {
		if k >= 1 {
			array = append(array, v.title)
		}
	}
	return path.Join(array...)
}

func genNavigatePath(levelCount int) string {
	return fmt.Sprintf(`#bag-list-wrapper > div:nth-child(1) > div.src-FileList-packages-ListHeader-header-styles-module__header-wrapper--2wyop > div.src-FileList-packages-ListHeader-header-styles-module__breadcrumb-container--H8gCj > div > span:nth-child(%d) > span.ant-breadcrumb-link > span`,
		levelCount)
}

func getQRCodeBase64(ydContext *YDNoteContext, sel string) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		var imgSrc string
		// 等待获取url
		for i := 0; i < 100; i++ {
			var nodes []*cdp.Node
			if err = chromedp.Nodes(sel, &nodes, chromedp.NodeReady, chromedp.NodeEnabled).Do(ctx); err != nil {
				log.Error().Err(err).Msg("获取二维码失败")
				return
			}

			imgSrc = nodes[0].AttributeValue("src")
			if len(imgSrc) != 0 {
				log.Info().Str("src", imgSrc[:30]).Int("loop", i).Msg("获取到二维码地址")
				break
			}
			time.Sleep(time.Millisecond * 10)
		}

		// 下载图片到本地
		// src包含图片内容， like 'data:image/png;base64,iVBORw0KGgo'
		imgData := strings.TrimPrefix(imgSrc, "data:image/png;base64,")
		sEnc, err := base64.StdEncoding.DecodeString(imgData)
		if err != nil {
			return err
		}
		// img, err := parseImg(ydContext, imgSrc, string(sEnc))
		img, _, err := image.Decode(bytes.NewReader(sEnc))
		if err != nil {
			return fmt.Errorf("image formt(%s) err(%w)", "dummy", err)
		}

		// log.Info().Str("file", localCacheDir(pngFile)).Msg("下载图片完成")
		if err = printQRCode(img); err != nil {
			return err
		}
		return
	}
}

func findAttr(attrs []string, attr string) (string, bool) {
	for k := 0; k < len(attrs)/2; k++ {
		if attrs[k*2] == attr {
			return attrs[k*2+1], true
		}
	}
	return "", false
}

func listChild(sel interface{}, children *[]*cdp.Node, opts ...chromedp.QueryOption) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		err = chromedp.Sleep(time.Second).Do(ctx)
		if err != nil {
			return err
		}

		var nodes []*cdp.Node
		err = chromedp.Nodes(sel,
			&nodes, opts...).Do(ctx)
		if err != nil {
			return err
		}

		if len(opts) > 0 {
			*children = nodes
			return nil
		}

		err = chromedp.ActionFunc(func(c context.Context) error {
			// depth -1 for the entire subtree
			// do your best to limit the size of the subtree
			return dom.RequestChildNodes(nodes[0].NodeID).WithDepth(3).Do(c)
		}).Do(ctx)
		if err != nil {
			return err
		}
		// wait a little while for dom.EventSetChildNodes to be fired and handled
		err = chromedp.Sleep(time.Second).Do(ctx)
		if err != nil {
			return err
		}

		if int(nodes[0].ChildNodeCount) > len(nodes[0].Children) {
			panic("child not ready")
		}

		// log.Info().
		// 	Int64("expect_count", nodes[0].ChildNodeCount).
		// 	Int("real_count", len(nodes[0].Children)).
		// 	Msg(`进入下载页面`)
		*children = nodes[0].Children
		return
	}
}

// 每个folderItem 也是多个node组成
func fetchFolderItemInfo(parentNode *cdp.Node, nameNode **cdp.Node, downloadNode **cdp.Node) {
	for _, v := range parentNode.Children {
		if cls, ok := findAttr(v.Attributes, "class"); ok && strings.Contains(cls, "module__name") {
			*nameNode = v.Children[0]
		} else if cls, ok := findAttr(v.Attributes, "class"); ok && strings.Contains(cls, "module__action-list") {
			*downloadNode = v.Children[0]
		}
	}
}

func findNodeBySel(outNode **cdp.Node, sel string) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		var nodes []*cdp.Node
		if err = chromedp.Nodes(sel, &nodes, chromedp.NodeReady, chromedp.NodeEnabled).Do(ctx); err != nil {
			return
		}
		if len(nodes) != 1 {
			err = fmt.Errorf("fail to find node:%v", sel)
			return
		}
		*outNode = nodes[0]
		return
	}
}

func waitUntil(begin time.Time, duration time.Duration, ctx context.Context, cancelFunc func() bool) {
	var costTime time.Duration
	for {
		_ = chromedp.Sleep(time.Microsecond).Do(ctx)
		costTime = time.Since(begin)

		if cancelFunc() || costTime >= duration {
			break
		}
	}
}

// 列出所有根目录内容，并且下载所有文件
func beginDownloadFile(ydContext *YDNoteContext, iterCache *inteStruct) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		err = chromedp.Sleep(time.Second).Do(ctx)
		if err != nil {
			return
		}

		// ss := fmt.Sprintf("key=%s;secret=%s;signature=%s",
		// 	signInfo.Key,
		// 	signInfo.Secret,
		// 	signInfo.Signature)
		// err = network.SetExtraHTTPHeaders(map[string]interface{}{
		// 	"X-Content-Security": ss,
		// 	"X-User-Id":          signInfo.UserID,
		// 	"Authorization":      authorization}).Do(ctx)
		// if err != nil {
		// 	panic(err)
		// }

		var folderItems []*cdp.Node
		reloadChild := func() {
			err = listChild("#bag-list-wrapper > div.src-FileList-packages-ListBody-styles-module__list-wrapper--3bkKg.src-FileList-packages-ListBody-styles-module__visible--1snrQ",
				&folderItems).Do(ctx)
			if err != nil {
				log.Error().Err(err).Msg("list child fail")
				return
			}
		}
		reloadChild()

		rootPath := iterCache.joinPath()
		if len(folderItems) > 0 {
			beginIndex := iterCache.getCurLevelIndex().nextIndex
			for k := beginIndex; k < len(folderItems); k++ {
				v := folderItems[k]
				if cls, ok := findAttr(v.Attributes, "class"); ok {
					var nameNode *cdp.Node
					var downloadNode *cdp.Node
					fetchFolderItemInfo(v, &nameNode, &downloadNode)
					if nameNode == nil || downloadNode == nil {
						continue
					}

					// 禁止悬浮，会影响点击
					var executed *runtime.RemoteObject
					_ = chromedp.Evaluate(`
					var clsInsts = document.getElementsByClassName('ant-tooltip ant-tooltip-placement-top');
					var boxes = Array.from(clsInsts);
					boxes.forEach(box => {
						box.remove();
					});
					`, executed).Do(ctx)
					// if err != nil {
					// 	fmt.Println(err)
					// }

					if strings.Contains(cls, "FolderItem") {
						// _ = chromedp.Sleep(time.Second/20 + time.Duration(rand.Int31n(1000))*time.Millisecond).Do(ctx)
						log.Info().Str("path", path.Join(rootPath, nameNode.NodeValue)).
							Int("child_index", k).
							Msg("enter folder")
						iterCache.getCurLevelIndex().nextIndex = k
						// 文件夹 src-FileList-packages-FolderItem-styles-module__file-item--3EHlz
						// 点击进入下一层目录
						// err = dom.Focus().WithNodeID(nameNode.NodeID).Do(ctx)
						// if err != nil {
						// 	fmt.Println("focus err", err)
						// }
						err = chromedp.MouseClickNode(nameNode).Do(ctx)
						if err != nil {
							log.Error().Err(err).Msg("click name node fail")
							return
						}

						// var dummyItems []*cdp.Node
						// tooltipSel := `body > div:nth-child(11) > div > div > div > div.ant-tooltip-arrow`
						// err = chromedp.Nodes(tooltipSel,
						// 	&dummyItems).Do(ctx)
						// if err != nil {
						// 	return
						// }
						// if len(dummyItems) > 0 {
						// 	err = chromedp.SetAttributes(tooltipSel, map[string]string{
						// 		"disabled":   "true",
						// 		"visibility": "hidden"}).Do(ctx)
						// 	if err != nil {
						// 		return
						// 	}
						// }

						// 等待跳转成功
						wantLevelSel := genNavigatePath(iterCache.cacheLevelCount() + 1)
						waitUntil(time.Now(), time.Second*15, ctx, func() bool {
							var levelNode *cdp.Node
							err = findNodeBySel(&levelNode, wantLevelSel).Do(ctx)
							if err != nil {
								return false
							}

							if len(levelNode.Children) == 0 || levelNode.Children[0].NodeValue != nameNode.NodeValue {
								return false
							}
							return true
						})

						// for i := 0; i < 30; i++ {
						// 	err = chromedp.Sleep(time.Second / 10).Do(ctx)
						// 	if err != nil {
						// 		continue
						// 	}

						// 	break
						// }

						subIterCache := &inteStruct{}
						*subIterCache = *iterCache
						subIterCache.AppendLevel(nameNode.NodeValue, 0)
						err = beginDownloadFile(ydContext, subIterCache).Do(ctx)
						if err != nil {
							log.Error().Err(err).Msg("begin download file fail")
							return
						}
					} else if strings.Contains(cls, "FileItem") {
						rp := localDownloadPath(rootPath, nameNode.NodeValue)
						if _, existErr := os.Stat(rp); existErr == nil {
							log.Info().Str("path", rp).Str("reason", "file exist").Msg("download skip")
						} else {
							log.Info().Str("path", rp).Msg("download prepare")
							// 可下载文件 src-FileList-packages-FileItem-styles-module__file-item--1jWXH
							// 直接下载
							waitUntil(time.Now(), time.Second*300, ctx, func() bool {
								if atomic.LoadInt32(&downloadingCount) >= 12 {
									chromedp.Sleep(time.Second).Do(ctx)
									log.Info().Str("path", rp).
										Msg("wait previous file to download finish")
									return false
								}

								if dupFile, loaded := downloadFileRelativePath.LoadOrStore(nameNode.NodeValue, rp); loaded {
									log.Info().Str("path", rp).Interface("duplicated file", dupFile).
										Msg("wait duplicated file to download finish")
									chromedp.Sleep(time.Second).Do(ctx)
									return false
								}
								return true
							})

							atomic.AddInt32(&downloadingCount, 1)
							err = chromedp.MouseClickNode(downloadNode).Do(ctx)
							if err != nil {
								log.Error().Err(err).Msg("click download fail")
								return
							}
						}
					} else {
						log.Info().Str("path", path.Join(rootPath, nameNode.NodeValue)).Msg("skip unknown format item")
					}
				}
			}

			// iterCache.popLevel()
			if iterCache.cacheLevelCount() > 1 {
				log.Info().Str("path", rootPath).Msg("the level iter ok")
				levelSel := genNavigatePath(iterCache.cacheLevelCount() - 1)
				var levelNode *cdp.Node
				err = findNodeBySel(&levelNode, levelSel).Do(ctx)
				if err != nil {
					log.Error().Err(err).Msg("find level navigate node fail")
					return
				}
				err = chromedp.MouseClickNode(levelNode).Do(ctx)
				if err != nil {
					log.Error().Err(err).Msg("navigate to up level fail")
					return
				}

				// 等待跳转成功
				err = chromedp.WaitNotPresent(levelNode).Do(ctx)
				if err != nil {
					return
				}
			} else {
				log.Info().Msg("job done, wait to all downloading task finish...")
				waitUntil(time.Now(), time.Second*30, ctx, func() bool {
					if atomic.LoadInt32(&downloadingCount) > 0 {
						return false
					}
					return true
				})
				log.Info().Msg("job done, wait to all downloading task finish ok")
			}
		}
		return
	}
}

// 切换到下载页面
func navigateToDownloadPage(ydContext *YDNoteContext) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		err = chromedp.WaitVisible(`#nav > div._1nJ7ryw1BkPUB-yg_R7h3h > div._3xgiAvLJejCh-_LxDwiSIw > a.uMNg0XNUc4lCYIxWH30Ft`).Do(ctx)
		if err != nil {
			return
		}

		// 点击班级
		log.Info().Msg(`选中 "班级"`)
		// #nav > div._1nJ7ryw1BkPUB-yg_R7h3h > div._3xgiAvLJejCh-_LxDwiSIw > a.uMNg0XNUc4lCYIxWH30Ft
		// #nav > div._1nJ7ryw1BkPUB-yg_R7h3h > div._3xgiAvLJejCh-_LxDwiSIw > a.uMNg0XNUc4lCYIxWH30Ft
		// #nav > div._1nJ7ryw1BkPUB-yg_R7h3h > div._3xgiAvLJejCh-_LxDwiSIw > a:nth-child(4)
		// #nav > div._1nJ7ryw1BkPUB-yg_R7h3h > div._3xgiAvLJejCh-_LxDwiSIw > a:nth-child(4)
		if err = chromedp.Click(`#nav > div._1nJ7ryw1BkPUB-yg_R7h3h > div._3xgiAvLJejCh-_LxDwiSIw > a:nth-child(4)`).Do(ctx); err != nil {
			return
		}

		log.Info().Msg(`选中 "班级文件"`)
		// 点击班级文件
		err = chromedp.Click(`#right-side-content > div > div > div.src-containers-Index-index__container--3F3Eq > div.src-containers-Operations-index__operations--7osDx > p:nth-child(2) > i`).Do(ctx)
		if err != nil {
			return
		}

		iterCache := &inteStruct{}
		iterCache.AppendLevel("root", 0)
		err = beginDownloadFile(ydContext, iterCache).Do(ctx)
		if err != nil {
			return err
		}
		// var html string
		// err = chromedp.OuterHTML("#bag-list-wrapper > div.src-FileList-packages-ListBody-styles-module__list-wrapper--3bkKg.src-FileList-packages-ListBody-styles-module__visible--1snrQ", &html, chromedp.ByQuery).Do(ctx)
		// if err != nil {
		// 	return
		// }

		return
	}
}

// 加载Cookies
func loadCookies(ydContext *YDNoteContext, cf string) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		// 如果cookies临时文件不存在则直接跳过
		if _, inErr := os.Stat(cf); os.IsNotExist(inErr) {
			return
		}

		// 如果存在则读取cookies的数据
		cookiesData, err := ioutil.ReadFile(cf)
		if err != nil {
			return
		}

		// 反序列化
		if err = ydContext.Cookies.UnmarshalJSON(cookiesData); err != nil {
			return
		}
		log.Info().Msg("加载到cookie")

		// 需要额外设置头
		// var token = `eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJleHAiOjE2NTk3NzkzOTAsImlhdCI6MTY1NzM2MDE5MCwidXNyIjoiNjI4MjI5YWU5YzM5MzEwMDAxMDIwZjBlIiwidmVyIjowfQ.6DuD9YlF25iabuedOsoX-jCwzeITDESaYatgMOVJsZE`
		// err = network.SetExtraHTTPHeaders(map[string]interface{}{"authority": token}).Do(ctx)
		// if err != nil {
		// 	return
		// }

		// 设置cookies
		return network.SetCookies(ydContext.Cookies.Cookies).Do(ctx)
	}
}

func waitScanQRCode(ydContext *YDNoteContext) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		cookies, err := network.GetAllCookies().Do(ctx)
		log.Info().Err(err).Msg("开始获取cookie")
		if err != nil {
			return
		}

		// 2. 序列化
		cookiesData, err := network.GetAllCookiesReturns{Cookies: cookies}.MarshalJSON()
		if err != nil {
			return
		}

		// 3. 存储到临时文件
		log.Info().Err(err).Str("file", cookieFile).RawJSON("cookie", cookiesData).Msg("存储cookie")
		if err = os.WriteFile(localCacheDir(cookieFile), cookiesData, 0755); err != nil {
			return
		}

		err = loadCookies(ydContext, localCacheDir(cookieFile)).Do(ctx)
		if err != nil {
			return
		}

		// // 获取accesstoken
		// sslcert, err := network.GetCertificate(entryURL).Do(ctx)
		// if err != nil {
		// 	return err
		// }
		// sDec, err := base64.StdEncoding.DecodeString(sslcert[0])
		// if err != nil {
		// 	return err
		// }
		// log.Info().Str("certificate", string(sDec)).Msg("get certificate")

		return
	}
}

func printQRCode(img image.Image) (err error) {
	// 使用gozxing库解码图片获取二进制位图
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		err = fmt.Errorf("二维码-gozxing解码错误(%w)", err)
		return
	}

	// 用二进制位图解码获取gozxing的二维码对象
	res, err := qrcode.NewQRCodeReader().Decode(bmp, nil)
	if err != nil {
		err = fmt.Errorf("二维码-qrcode解码错误(%w)", err)
		return
	}

	log.Info().Str("qrcode", res.String()).Msg("qrcode str")
	// config := qrterminal.Config{
	// 	Level:     qrterminal.M,
	// 	Writer:    os.Stdout,
	// 	BlackChar: qrterminal.BLACK,
	// 	WhiteChar: qrterminal.WHITE,
	// 	QuietZone: 1,
	// }
	// if runtime.GOOS == "windows" {
	// 	config.Writer = colorable.NewColorableStdout()
	// 	// config.BlackChar = qrterminal.BLACK
	// 	// config.WhiteChar = qrterminal.WHITE
	// }
	qrterminal.Generate(res.String(), qrterminal.M, terminalWriter)
	return
}

// {"key":"WMuQ","secret":"JlAj91xyJoArxpmbaD1FAmrvVVFNkP4opEzEnc86oiDnLaA1B94N1huteqCH5nLqRl3Q1iBBFYGKve5YqWx+G6MfLvod8uDgIm0xGGMjWo4DWNOVch0fKkHNySbTF1M6LoMTHy/XsWIKpvMVMO+MmZUz5B5doh/E5a+k/bevLpU=","signature":"jA4KkOqqdld5vHDESObgsG3LQIlK4202+/5pdMcfmFE="}&appId=1&desc=班级&pageId=50011&typeId=50011.2&type=2&_listen=true&userId=628229ae9c39310001020f0e&createTime=1657361024001&path=/message&os=web&blackboardVersion= 200 OK map[]  application/json map[]  false 685 47.110.172.25 443 false false false 463 0xc0001f8240  0xc0002f35f0  http/1.1 secure 0xc000190ea0}

type signStruct struct {
	Key       string
	Secret    string
	Signature string

	UserID string
}

// 登陆
func doLogin(ydContext *YDNoteContext, url string) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		err = loadCookies(ydContext, localCacheDir(cookieFile)).Do(ctx)
		if err != nil {
			return
		}

		err = chromedp.Navigate(url).Do(ctx)
		if err != nil {
			return
		}

		// // 小黑板总会重定向到登陆界面
		// time.Sleep(time.Second * 3)

		var url string
		if chromedp.Location(&url).Do(ctx); err != nil {
			return
		}

		log.Info().Str("url", url).Msg("window.location.href")

		// 不是登陆界面，说明已经登陆成功
		if !strings.Contains(url, loginURL) {
			log.Info().Msg("使用cookies登陆成功")
			chromedp.Stop()
			return
		}

		// 不在登陆状态，使用二维码登陆
		log.Info().Msg("没有cookie，开始晓黑板登陆...")
		if err = doQRCodeLogin(ydContext).Do(ctx); err != nil {
			return
		}

		if err = chromedp.WaitVisible(loginFinishSel).Do(ctx); err != nil {
			return
		}
		log.Info().Msg("扫码完成")

		if err = waitScanQRCode(ydContext).Do(ctx); err != nil {
			return
		}
		return
	}
}

// 二维码登陆
func doQRCodeLogin(ydContext *YDNoteContext) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		// err = chromedp.Click(loginBtnSelector).Do(ctx)
		// if err != nil {
		// 	log.Error().Err(err).Msg("晓黑板登陆 二维码按钮点击失败")
		// 	return
		// }

		// 获得新打开的第一个非空页面-晓黑板登陆（晓黑板登陆另起一个新页面）
		// ch := chromedp.WaitNewTarget(ctx, func(info *target.Info) bool {
		// 	if info.URL != "" {
		// 		log.Info().Str("url", info.URL).Msg("晓黑板登陆界面已经打开")
		// 		return true
		// 	}
		// 	return false
		// })

		// 等待晓黑板登陆页签打开
		// newCtx, cancel := chromedp.NewContext(ctx, chromedp.WithTargetID(<-ch))
		// defer cancel()
		// fmt.Println("begin hdl qr code")
		// if err := chromedp.Run(newCtx,
		// 	hdlQRCode(ydContext, loginQRCodeSelector)); err != nil {
		// 	log.Fatal().Err(err).Msg("")
		// }

		err = getQRCodeBase64(ydContext, loginQRCodeSelector).Do(ctx)
		if err != nil {
			log.Error().Err(err).Msg("获取二维码失败")
			return
		}

		log.Info().Msg("等待扫码...")
		return
	}
}

// 处理二维码
// func hdlQRCode(ydContext *YDNoteContext, sel string) chromedp.Tasks {
// 	fmt.Println("wait qr code", loginQRCodeSelector)
// 	return chromedp.Tasks{
// 		chromedp.WaitReady(sel, chromedp.ByQuery),
// 		// 获取并且打印二维码
// 		getQRCode(ydContext, sel),
// 		chromedp.WaitReady("#close", chromedp.ByQuery),
// 		waitScanQRCode(ydContext, "#close"),
// 	}
// }

func doDownload(ydContext *YDNoteContext, onLoginOk func(*YDNoteContext)) chromedp.ActionFunc {
	return func(ctx context.Context) (err error) {
		log.Info().Msg("开始下载...")
		err = navigateToDownloadPage(ydContext).Do(ctx)
		if err != nil {
			return err
		}

		if onLoginOk != nil {
			onLoginOk(ydContext)
		}
		return
	}
}

func doYoudaoNoteLogin(ydContext *YDNoteContext, host string, onLoginOk func(*YDNoteContext)) {
	ydHeadless = false
	ctx, _ := chromedp.NewExecAllocator(
		ydContext.Context,

		// 以默认配置的数组为基础，覆写headless参数
		// 当然也可以根据自己的需要进行修改，这个flag是浏览器的设置
		append(chromedp.DefaultExecAllocatorOptions[:], chromedp.Flag("headless", ydHeadless))...,
	)
	ctx, _ = chromedp.NewContext(
		ctx,
		// 设置日志方法
		chromedp.WithLogf(log.Printf),
	)

	// 打开有道官网
	log.Info().Str("url", host).Msg("begin navigate")
	if err := chromedp.Run(ctx,
		doLogin(ydContext, host)); err != nil {
		log.Fatal().Err(err).Send()
		return
	}

	// 设置下载监听事件
	// var pendingCount int32
	// done := make(chan string, 10)
	// set up a listener to watch the download events and close the channel
	// when complete this could be expanded to handle multiple downloads
	// through creating a guid map, monitor download urls via
	// EventDownloadWillBegin, etc
	chromedp.ListenTarget(ctx, func(v interface{}) {
		switch ev := v.(type) {
		case *network.EventSignedExchangeReceived:
			log.Info().Str("RequestID", string(ev.RequestID)).Interface("Info", ev.Info).Msg("event signed exhanged received")
		case *network.EventResponseReceived:
			break
			u, err := url.Parse(ev.Response.URL)
			if err != nil {
				panic(err)
			}
			vals, err := url.ParseQuery(u.RawQuery)
			if err != nil {
				panic(err)
			}

			log.Info().Str("raw_query", u.RawQuery).Msg("permision")
			if signInfo == nil || len(signToken) == 0 {
				if sign, ok := vals["sign"]; ok {
					signInfo = &signStruct{}
					err := json.Unmarshal([]byte(sign[0]), &signInfo)
					if err != nil {
						panic(err)
					}

					signInfo.UserID = vals["userId"][0]

					// if s, err := base64.URLEncoding.DecodeString(signToken.Secret); err == nil {
					// 	signToken.Secret = string(s)
					// }
					// if s, err := base64.URLEncoding.DecodeString(signToken.Signature); err == nil {
					// 	signToken.Signature = string(s)
					// }
				}
				if token, ok := vals["token"]; ok {
					signToken = token[0]
				}
			}

		case *browser.EventDownloadWillBegin:
			log.Info().Interface("GUID", ev.GUID).Str("SuggestedFilename", ev.SuggestedFilename).Msg("download begin")
			downloadingFiles.Store(ev.GUID, ev.SuggestedFilename)

		case *browser.EventDownloadProgress:
			if ev.State == browser.DownloadProgressStateCompleted {
				if loadVal, ok := downloadingFiles.LoadAndDelete(ev.GUID); ok {
					// _, _ = <-downloadBucket
					filename, _ := loadVal.(string)
					atomic.AddInt32(&downloadingCount, -1)

					if relativePath, relativeOK := downloadFileRelativePath.LoadAndDelete(filename); relativeOK {
						cachePath, _ := filepath.Abs(localDownloadPath(filename))
						targetPath, _ := filepath.Abs(relativePath.(string))
						log.Info().Str("state", ev.State.String()).Str("GUID", ev.GUID).
							Str("cache_path", cachePath).
							Str("target_path", targetPath).
							Msg("download finish")

						err := os.Rename(cachePath, targetPath)
						if err != nil {
							log.Error().Err(err).Send()
						}
					} else {
						log.Error().Interface("file", filename).Msg("fail to find file relative path")
					}
				}
			}

		case *network.EventRequestWillBeSentExtraInfo:
			// log.Info().Interface("event", ev).Msg("request extra info")
			if a, ok := ev.Headers["Authorization"].(string); ok {
				authorization = a
			}

		default:
			// log.Info().Interface("event", v).Msg("default")
		}
	})

	// 等待登陆完成
	fp, _ := filepath.Abs(localDownloadPath())
	log.Info().Str("path", fp).Msg("set local download path")
	if err := chromedp.Run(ctx,
		network.Enable(),
		browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
			WithDownloadPath(fp).
			WithEventsEnabled(true),
		doDownload(ydContext, onLoginOk)); err != nil {
		log.Fatal().Err(err).Send()
		return
	}

	time.Sleep(time.Second * 5)

	log.Info().Str("host", host).Msg("end navigate")
}
