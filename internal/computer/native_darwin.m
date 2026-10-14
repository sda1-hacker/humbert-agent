//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <ApplicationServices/ApplicationServices.h>
#import <ScreenCaptureKit/ScreenCaptureKit.h>
#import <ImageIO/ImageIO.h>

// 只列出普通可见窗口；自己的审批窗口不能成为模型的操作目标。
char *hc_windows(void) {
    @autoreleasepool {
        NSArray *items = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
        NSMutableArray *result = [NSMutableArray array];
        CGDirectDisplayID displays[16]; uint32_t displayCount=0;
        if (CGGetActiveDisplayList(16,displays,&displayCount)==kCGErrorSuccess) for(uint32_t i=0;i<displayCount;i++) {
            CGRect rect=CGDisplayBounds(displays[i]);
            [result addObject:@{@"id":[NSString stringWithFormat:@"screen:%u",displays[i]],@"app":@"桌面（包含菜单与所有可见应用）",@"title":[NSString stringWithFormat:@"屏幕 %u",i+1],@"x":@((int)rect.origin.x),@"y":@((int)rect.origin.y),@"width":@((int)rect.size.width),@"height":@((int)rect.size.height)}];
        }
        for (NSDictionary *item in items) {
            int pid = [item[(id)kCGWindowOwnerPID] intValue];
            CGRect rect;
            if (pid == getpid() || [item[(id)kCGWindowLayer] intValue] != 0 || !CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)item[(id)kCGWindowBounds], &rect) || rect.size.width < 40 || rect.size.height < 40 || rect.size.width > 8192 || rect.size.height > 8192) continue;
            NSRunningApplication *app = [NSRunningApplication runningApplicationWithProcessIdentifier:pid];
            NSString *identity = [NSString stringWithFormat:@"%u:%d:%.0f", [item[(id)kCGWindowNumber] unsignedIntValue], pid, app.launchDate.timeIntervalSince1970 * 1000];
            [result addObject:@{@"id": identity, @"app": app.bundleIdentifier ?: item[(id)kCGWindowOwnerName] ?: @"", @"title": item[(id)kCGWindowName] ?: @"", @"x": @((int)rect.origin.x), @"y": @((int)rect.origin.y), @"width": @((int)rect.size.width), @"height": @((int)rect.size.height)}];
            if (result.count >= 80) break;
        }
        NSData *json = [NSJSONSerialization dataWithJSONObject:result options:0 error:nil];
        if (!json) return NULL;
        char *text = malloc(json.length + 1);
        if (!text) return NULL;
        memcpy(text, json.bytes, json.length);
        text[json.length] = 0;
        return text;
    }
}

int hc_permissions(void) {
    return (CGPreflightScreenCaptureAccess() ? 1 : 0) | (AXIsProcessTrusted() ? 2 : 0);
}

int hc_focus(int pid, int x, int y, int w, int h) {
    @autoreleasepool {
        AXUIElementRef app = AXUIElementCreateApplication(pid);
        AXUIElementSetMessagingTimeout(app, 0.5);
        CFArrayRef windows = NULL;
        int raised = 0;
        if (AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, (CFTypeRef *)&windows) == kAXErrorSuccess && windows) {
            for (CFIndex i = 0; i < MIN(CFArrayGetCount(windows), 80); i++) {
                AXUIElementRef window = (AXUIElementRef)CFArrayGetValueAtIndex(windows, i);
                AXValueRef position = NULL, size = NULL;
                CGPoint p = CGPointZero; CGSize s = CGSizeZero;
                AXUIElementCopyAttributeValue(window, kAXPositionAttribute, (CFTypeRef *)&position);
                AXUIElementCopyAttributeValue(window, kAXSizeAttribute, (CFTypeRef *)&size);
                bool match = position && size && AXValueGetValue(position, kAXValueCGPointType, &p) && AXValueGetValue(size, kAXValueCGSizeType, &s) && fabs(p.x-x) < 2 && fabs(p.y-y) < 2 && fabs(s.width-w) < 2 && fabs(s.height-h) < 2;
                if (position) CFRelease(position);
                if (size) CFRelease(size);
                if (match) {
                    raised = AXUIElementPerformAction(window, kAXRaiseAction) == kAXErrorSuccess;
                    AXUIElementSetAttributeValue(window, kAXMainAttribute, kCFBooleanTrue);
                    break;
                }
            }
            CFRelease(windows);
        }
        CFRelease(app);
        if (raised) [[NSRunningApplication runningApplicationWithProcessIdentifier:pid] activateWithOptions:NSApplicationActivateIgnoringOtherApps];
        return raised;
    }
}

bool hc_front(unsigned int number, int pid) {
    @autoreleasepool {
        if (NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier != pid) return false;
        NSArray *items = CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID));
        for (NSDictionary *item in items) {
            if ([item[(id)kCGWindowLayer] intValue] == 0 && [item[(id)kCGWindowOwnerPID] intValue] == pid)
                return [item[(id)kCGWindowNumber] unsignedIntValue] == number;
        }
        return false;
    }
}

// macOS 14+ 使用 ScreenCaptureKit；12/13 保留系统旧截图 API，避免抬高应用最低版本。
// 异步回调只持有 Objective-C 对象，不保存 Go 指针；超时后到达的回调不会访问已释放内存。
void *hc_capture(unsigned int number, bool screen, int width, int height, int *size) {
    @autoreleasepool {
        __block NSData *png = nil;
        void (^encode)(CGImageRef) = ^(CGImageRef image) {
            if (!image) return;
            NSMutableData *data = [NSMutableData data];
            CGImageDestinationRef dest = CGImageDestinationCreateWithData((__bridge CFMutableDataRef)data, CFSTR("public.png"), 1, NULL);
            if (dest) {
                CGImageDestinationAddImage(dest, image, NULL);
                if (CGImageDestinationFinalize(dest)) png = data;
                CFRelease(dest);
            }
        };
        if (@available(macOS 14.0, *)) {
            dispatch_semaphore_t done = dispatch_semaphore_create(0);
            [SCShareableContent getShareableContentExcludingDesktopWindows:YES onScreenWindowsOnly:YES completionHandler:^(SCShareableContent *content, NSError *error) {
                SCContentFilter *filter = nil;
                if(screen){
                    for(SCDisplay *display in content.displays) if(display.displayID==number){filter=[[SCContentFilter alloc] initWithDisplay:display excludingWindows:@[]];break;}
                }else{
                    for(SCWindow *window in content.windows) if(window.windowID==number){filter=[[SCContentFilter alloc] initWithDesktopIndependentWindow:window];break;}
                }
                if (!filter || error) { dispatch_semaphore_signal(done); return; }
                SCStreamConfiguration *config = [SCStreamConfiguration new];
                config.width = width; config.height = height;
                config.showsCursor = NO; config.ignoreShadowsSingleWindow = YES;
                [SCScreenshotManager captureImageWithFilter:filter configuration:config completionHandler:^(CGImageRef image, NSError *captureError) {
                    if (!captureError) encode(image);
                    dispatch_semaphore_signal(done);
                }];
            }];
            if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, 3 * NSEC_PER_SEC))) return NULL;
        } else {
            CGImageRef image = screen?CGDisplayCreateImage(number):CGWindowListCreateImage(CGRectNull, kCGWindowListOptionIncludingWindow, number, kCGWindowImageBoundsIgnoreFraming | kCGWindowImageNominalResolution);
            encode(image);
            if (image) CGImageRelease(image);
        }
        if (!png || png.length > 32 * 1024 * 1024) return NULL;
        *size = (int)png.length;
        void *result = malloc(png.length);
        if (result) memcpy(result, png.bytes, png.length);
        return result;
    }
}

bool hc_input_allowed(void){return NSWorkspace.sharedWorkspace.frontmostApplication.processIdentifier!=getpid() && AXIsProcessTrusted();}
bool hc_point_allowed(int x,int y,int pid){
    @autoreleasepool{
        NSArray *items=CFBridgingRelease(CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly|kCGWindowListExcludeDesktopElements,kCGNullWindowID));
        for(NSDictionary *item in items){
            CGRect rect;
            if([item[(id)kCGWindowLayer] intValue]<0||!CGRectMakeWithDictionaryRepresentation((CFDictionaryRef)item[(id)kCGWindowBounds],&rect)||!CGRectContainsPoint(rect,CGPointMake(x,y)))continue;
            int owner=[item[(id)kCGWindowOwnerPID] intValue];
            return owner!=getpid() && (!pid || owner==pid);
        }
        return pid==0;
    }
}

bool hc_emergency(void) {
    CGEventFlags flags = CGEventSourceFlagsState(kCGEventSourceStateHIDSystemState);
    return (flags & kCGEventFlagMaskControl) && (flags & kCGEventFlagMaskAlternate) && CGEventSourceKeyState(kCGEventSourceStateHIDSystemState, 53);
}
