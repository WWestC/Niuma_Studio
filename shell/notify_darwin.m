#import <AppKit/AppKit.h>
#import <UserNotifications/UserNotifications.h>

// notify_darwin.m — 原生系统通知（微信/飞书式 OS 弹窗）的 ObjC 半边：
// WKWebView 没有 Web Notification API，壳里经 UNUserNotificationCenter
// 代发。四个入口（Go 侧 notify_darwin.go 包 cgo 前言声明）：
//   niumaNotifySetup  主线程一次性装 delegate＋请求授权（弹一次系统
//                     「牛马工作室想给你发通知」；拒过则静默，投递会被
//                     系统丢弃——改主意去 系统设置→通知）
//   niumaNotifyAskAuth  按需重问授权（已定状态不弹窗、答现行新账——
//                     设置卡开关手势的礼貌问询时机）
//   niumaNotifyProbe   只读探现行授权（getNotificationSettings，绝不弹
//                     问询）——消息路径判通道生死用：被拒则页面走兜底
//   niumaNotifyPost   发一条：title/body/threadIdentifier（thread＝房键，
//                     通知中心按房分组折叠；identifier 同取房键——UN 的
//                     替换语义认 identifier，同房新弹窗顶掉旧的——微信同款）
// delegate 两职：
//   willPresent  应用在前台也让弹（.banner+.list）——「正看着的房不发」
//                由前端同未读徽标一道门把关，这里不重复判
//   didReceive   用户点了弹窗 → niumaNotifyClicked(thread) 回调进 Go，
//                Go 经 webview Eval 唤 window.__osNotifyClick(房键) 跳房
extern void niumaNotifyClicked(const char *thread);

@interface NiumaNotifyDelegate : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation NiumaNotifyDelegate
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
        willPresentNotification:(UNNotification *)notification
             withCompletionHandler:(void (^)(UNNotificationPresentationOptions))completionHandler {
	// 不带 .sound：提示音由页面自己的 HTMLAudio 出（叮咚/叮铃带冷却窗），
	// 系统再响一声就是双响
	completionHandler(UNNotificationPresentationOptionBanner | UNNotificationPresentationOptionList);
}
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
        didReceiveNotificationResponse:(UNNotificationResponse *)response
             withCompletionHandler:(void (^)(void))completionHandler {
	const char *thread = [response.notification.request.content.threadIdentifier UTF8String];
	if (thread != NULL) niumaNotifyClicked(thread);
	completionHandler();
}
@end

static NiumaNotifyDelegate *gNotifyDelegate = nil;
// 最近一次授权请求的答复（0 未得知 / 1 已允许 / 2 已拒绝）：setup 的
// requestAuthorization 回调里落账。拒绝后再也不弹系统问询（macOS 纪
// 律），页面的设置卡靠 osNotifyAuth 读它——被拒时给出「去系统设置打
// 开」的指路，而不是测一条永远不响的通知装作没事。
static volatile int gNotifyAuth = 0;

// UNUserNotificationCenter 在主 bundle 拿不出身份时（裸进程直跑内层
// 二进制、build.sh rm -rf 重打 bundle 的窗口期、Info.plist 损坏）从
// dispatch_once 块里抛 NSInternalInconsistencyException——异常穿不出
// libdispatch 的 client callout，@try 接不住，一抛就是 SIGABRT 全进程
// 暴毙（窗口＋房间服务一起没，2026-10-02 启动失败之夜的元凶）。所以
// 触碰它之前必须先自查：bundleIdentifier 拿不出就整体跳过通知——通
// 知是便利子系统，宁可哑，不许炸。
static BOOL niumaNotificationsAvailable(void) {
	NSString *bid = [[NSBundle mainBundle] bundleIdentifier];
	return bid != nil && [bid length] > 0;
}

void niumaNotifySetup(void) {
	if (gNotifyDelegate != nil) return; // 幂等：一进程一 delegate
	if (!niumaNotificationsAvailable()) return; // 无 bundle 身份：降级为无通知
	// 前置自查之外的意外抛错仍兜一手（@try 接不住 dispatch 块内的
	// 异常，但能接住其余路径）
	@try {
		UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
		NiumaNotifyDelegate *d = [[NiumaNotifyDelegate alloc] init];
		center.delegate = d;
		[center requestAuthorizationWithOptions:UNAuthorizationOptionAlert
		    completionHandler:^(BOOL granted, NSError *err) {
			    // 授权结果交给系统记账：拒了投递自灭，granted 我们不另存
			    // ——只留一枚最近答复供 osNotifyAuth 查（设置卡指路用）
			    gNotifyAuth = granted ? 1 : 2;
			    (void)err;
		    }];
		gNotifyDelegate = d; // 只在整套装成后置位：失败则 post 全部哑掉
	} @catch (NSException *e) {
		gNotifyDelegate = nil;
	}
}

// 按需重问授权（页面 osNotifyAuth 桥背后就是它）：已定状态（允许/
// 拒绝）下重问**不弹窗**、直接回现行答复——用户在 系统设置→通知 里
// 改了主意，这里拿到的是新账，不是启动那笔旧账。未定状态会弹系统
// 问询（与启动那次同款，答完落账）；2 秒等不到（用户还没点问询）按
// 旧账答——主线程阻塞有上限，宁可报 unknown 不吊死设置卡。
int niumaNotifyAskAuth(void) {
	if (gNotifyDelegate == nil) return 0; // 没装成（无 bundle 等降级）：不知情
	__block int answer = gNotifyAuth;
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	@try {
		UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
		[center requestAuthorizationWithOptions:UNAuthorizationOptionAlert
		    completionHandler:^(BOOL granted, NSError *err) {
			    gNotifyAuth = granted ? 1 : 2;
			    answer = gNotifyAuth;
			    dispatch_semaphore_signal(done);
			    (void)err;
		    }];
		dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC));
	} @catch (NSException *e) {
		// 静默：按旧账答，不许炸
	}
	return answer;
}

// 只读授权探针：getNotificationSettings 读现行授权，绝不 request
// Authorization——那会弹问询，而问询只许发生在用户手势上（设置卡开
// 开关的时机）。消息路径（页面的兜底横幅门）靠它判通道生死：被拒的
// 通道投了也是系统静默丢，与其每条消息扔进黑洞，不如探一次把话挑明。
// 答复口径与 gNotifyAuth 同账：0 未得知（含无 bundle 降级——不装知道）
// / 1 已允许（provisional 也算：横幅能弹）/ 2 已拒绝。
int niumaNotifyProbe(void) {
	if (gNotifyDelegate == nil) return 0; // 无 bundle 降级：不知情
	__block int answer = 0;
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	@try {
		UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
		[center getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings *s) {
			switch (s.authorizationStatus) {
				case UNAuthorizationStatusAuthorized:
				case UNAuthorizationStatusProvisional:
					answer = 1; break;
				case UNAuthorizationStatusDenied:
					answer = 2; break;
				default:
					answer = 0; break; // NotDetermined：启动那问还悬着没答
			}
			dispatch_semaphore_signal(done);
		}];
		dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 2 * NSEC_PER_SEC));
	} @catch (NSException *e) {
		// 静默：按未得知答，不许炸
	}
	return answer;
}

void niumaNotifyPost(const char *title, const char *body, const char *thread) {
	if (gNotifyDelegate == nil) return; // 没 setup 过或 setup 时无 bundle 身份（降级为无通知）
	// 同一把保护伞：投递路径上任何 UN 异常都不许带走进程——静默丢弃
	// 这一条即可
	@try {
		UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
		content.title = [NSString stringWithUTF8String:(title ?: "")];
		content.body = [NSString stringWithUTF8String:(body ?: "")];
		NSString *threadID = [NSString stringWithUTF8String:(thread ?: "")];
		content.threadIdentifier = threadID;
		// identifier＝房键（与 threadIdentifier 同源、稳定不变）：UN 的
		// 「新弹窗顶掉旧的」认 identifier，threadIdentifier 只管通知中心
		// 分组不替换——先前每条一个 NSUUID，同房通知永不互顶、按房无限
		// 堆积，调度器连珠炮时通知数量爆炸（微信是稳定 id 才做得到同款）
		NSString *ident = [@"niuma:" stringByAppendingString:(threadID.length ? threadID : @"room")];
		UNNotificationRequest *req = [UNNotificationRequest
			requestWithIdentifier:ident
		                      content:content
		                      trigger:nil]; // nil＝立即投递
		[[UNUserNotificationCenter currentNotificationCenter] addNotificationRequest:req withCompletionHandler:nil];
	} @catch (NSException *e) {
		// 静默：这一条通知没了，应用还在
	}
}
