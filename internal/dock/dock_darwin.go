//go:build darwin

package dock

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#import <dispatch/dispatch.h>

// setActivationPolicy AppKit 要求在主线程修改激活策略，
// 这里统一 dispatch 到主队列，避免从 Go goroutine 直接调用引发崩溃。
static void setActivationPolicy(long policy) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp setActivationPolicy:(NSApplicationActivationPolicy)policy];
    });
}

// setAccessory 隐藏 Dock 图标（菜单栏应用模式）
void setAccessory() {
    setActivationPolicy(NSApplicationActivationPolicyAccessory);
}

// setRegular 恢复 Dock 图标（普通应用模式）
void setRegular() {
    setActivationPolicy(NSApplicationActivationPolicyRegular);
}
*/
import "C"

// Hide 隐藏 Dock 图标
func Hide() {
	C.setAccessory()
}

// Show 恢复 Dock 图标
func Show() {
	C.setRegular()
}
