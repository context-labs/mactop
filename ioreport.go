// Copyright (c) 2024-2026 Carsen Klock under MIT License
// ioreport.go - Go wrappers for IOReport power/thermal metrics
package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreFoundation -framework IOKit -framework Foundation -lIOReport
#include <mach/mach_host.h>
#include <mach/processor_info.h>
#include <mach/mach_init.h>
#include <CoreFoundation/CoreFoundation.h>
#include <IOKit/IOKitLib.h>
#include <stdint.h>
#include <string.h>
#include <stdlib.h>

typedef struct IOReportSubscriptionRef* IOReportSubscriptionRef;

extern CFDictionaryRef IOReportCopyChannelsInGroup(CFStringRef group, CFStringRef subgroup, uint64_t a, uint64_t b, uint64_t c);
extern void IOReportMergeChannels(CFDictionaryRef a, CFDictionaryRef b, CFTypeRef unused);
extern IOReportSubscriptionRef IOReportCreateSubscription(void* a, CFMutableDictionaryRef channels, CFMutableDictionaryRef* out, uint64_t d, CFTypeRef e);
extern CFDictionaryRef IOReportCreateSamples(IOReportSubscriptionRef sub, CFMutableDictionaryRef channels, CFTypeRef unused);
extern CFDictionaryRef IOReportCreateSamplesDelta(CFDictionaryRef a, CFDictionaryRef b, CFTypeRef unused);
extern int64_t IOReportSimpleGetIntegerValue(CFDictionaryRef item, int32_t idx);
extern CFStringRef IOReportChannelGetGroup(CFDictionaryRef item);
extern CFStringRef IOReportChannelGetSubGroup(CFDictionaryRef item);
extern CFStringRef IOReportChannelGetChannelName(CFDictionaryRef item);
extern CFStringRef IOReportChannelGetUnitLabel(CFDictionaryRef item);
extern int32_t IOReportStateGetCount(CFDictionaryRef item);
extern CFStringRef IOReportStateGetNameForIndex(CFDictionaryRef item, int32_t idx);
extern int64_t IOReportStateGetResidency(CFDictionaryRef item, int32_t idx);

typedef void* IOHIDEventSystemClientRef;
typedef void* IOHIDServiceClientRef;
typedef void* IOHIDEventRef;

extern IOHIDEventSystemClientRef IOHIDEventSystemClientCreate(CFAllocatorRef allocator);
extern int IOHIDEventSystemClientSetMatching(IOHIDEventSystemClientRef client, CFDictionaryRef matching);
extern CFArrayRef IOHIDEventSystemClientCopyServices(IOHIDEventSystemClientRef client);
extern CFStringRef IOHIDServiceClientCopyProperty(IOHIDServiceClientRef service, CFStringRef key);
extern IOHIDEventRef IOHIDServiceClientCopyEvent(IOHIDServiceClientRef service, int64_t type, int32_t options, int64_t timeout);
extern double IOHIDEventGetFloatValue(IOHIDEventRef event, int64_t field);

typedef struct {
    double cpuPower;
    double gpuPower;
    double anePower;
    double dramPower;
    double gpuSramPower;
    double systemPower;
    int gpuFreqMHz;
    double gpuActive;
    float socTemp;
} PowerMetrics;

int initIOReport();
PowerMetrics samplePowerMetrics(int durationMs);
void cleanupIOReport();
int getThermalState();
*/
import "C"

type SocMetrics struct {
	CPUPower     float64 `json:"cpu_power"`
	GPUPower     float64 `json:"gpu_power"`
	ANEPower     float64 `json:"ane_power"`
	DRAMPower    float64 `json:"dram_power"`
	GPUSRAMPower float64 `json:"gpu_sram_power"`
	SystemPower  float64 `json:"system_power"`
	TotalPower   float64 `json:"total_power"`
	GPUFreqMHz   int32   `json:"gpu_freq_mhz"`
	GPUActive    float64 `json:"-"`
	SocTemp      float32 `json:"soc_temp"`
}

func initSocMetrics() error {
	if ret := C.initIOReport(); ret != 0 {
		return nil
	}
	return nil
}

func sampleSocMetrics(durationMs int) SocMetrics {
	pm := C.samplePowerMetrics(C.int(durationMs))
	return SocMetrics{
		CPUPower:     float64(pm.cpuPower),
		GPUPower:     float64(pm.gpuPower),
		ANEPower:     float64(pm.anePower),
		DRAMPower:    float64(pm.dramPower),
		GPUSRAMPower: float64(pm.gpuSramPower),
		SystemPower:  float64(pm.systemPower),
		TotalPower:   float64(pm.cpuPower) + float64(pm.gpuPower) + float64(pm.anePower) + float64(pm.dramPower) + float64(pm.gpuSramPower),
		GPUFreqMHz:   int32(pm.gpuFreqMHz),
		GPUActive:    float64(pm.gpuActive),
		SocTemp:      float32(pm.socTemp),
	}
}

func cleanupSocMetrics() {
	C.cleanupIOReport()
}

func getSocThermalState() int {
	return int(C.getThermalState())
}
