#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/IOMessage.h>
#include <IOKit/pwr_mgt/IOPMLib.h>

#include "power_darwin.h"

extern void peykPower(int kind, long id);

static io_connect_t root;

static void onPower(void *ref, io_service_t service, natural_t msg, void *arg) {
	switch (msg) {
	case kIOMessageCanSystemSleep:
		IOAllowPowerChange(root, (long)arg);
		break;
	case kIOMessageSystemWillSleep:
		peykPower(PEYK_WILL_SLEEP, (long)arg);
		break;
	case kIOMessageSystemHasPoweredOn:
		peykPower(PEYK_POWERED_ON, 0);
		break;
	}
}

int peyk_power_register(void) {
	IONotificationPortRef port;
	io_object_t notifier;
	root = IORegisterForSystemPower(NULL, &port, onPower, &notifier);
	if (root == MACH_PORT_NULL) return -1;
	CFRunLoopAddSource(CFRunLoopGetCurrent(), IONotificationPortGetRunLoopSource(port), kCFRunLoopCommonModes);
	return 0;
}

void peyk_power_loop(void) { CFRunLoopRun(); }

void peyk_power_allow(long id) { IOAllowPowerChange(root, id); }

int peyk_power_capabilities(void) {
	io_service_t r = IOServiceGetMatchingService(kIOMainPortDefault, IOServiceNameMatching("IOPMrootDomain"));
	if (r == MACH_PORT_NULL) return -1;
	CFTypeRef v = IORegistryEntryCreateCFProperty(r, CFSTR("System Capabilities"), kCFAllocatorDefault, 0);
	IOObjectRelease(r);
	int n = -1;
	if (v != NULL && CFGetTypeID(v) == CFNumberGetTypeID()) CFNumberGetValue((CFNumberRef)v, kCFNumberIntType, &n);
	if (v != NULL) CFRelease(v);
	return n;
}
