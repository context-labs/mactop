// Copyright (c) 2024-2026 Carsen Klock under MIT License
// ioreport.m - Objective-C implementation for IOReport power/thermal metrics

#include "smc.h"
#import <CoreFoundation/CoreFoundation.h>
#import <Foundation/Foundation.h>
#import <IOKit/IOKitLib.h>
#include <mach/mach_host.h>
#include <mach/mach_init.h>
#include <mach/processor_info.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

typedef struct IOReportSubscriptionRef *IOReportSubscriptionRef;

extern CFDictionaryRef IOReportCopyChannelsInGroup(CFStringRef group,
                                                   CFStringRef subgroup,
                                                   uint64_t a, uint64_t b,
                                                   uint64_t c);
extern void IOReportMergeChannels(CFDictionaryRef a, CFDictionaryRef b,
                                  CFTypeRef unused);
extern IOReportSubscriptionRef
IOReportCreateSubscription(void *a, CFMutableDictionaryRef channels,
                           CFMutableDictionaryRef *out, uint64_t d,
                           CFTypeRef e);
extern CFDictionaryRef IOReportCreateSamples(IOReportSubscriptionRef sub,
                                             CFMutableDictionaryRef channels,
                                             CFTypeRef unused);
extern CFDictionaryRef IOReportCreateSamplesDelta(CFDictionaryRef a,
                                                  CFDictionaryRef b,
                                                  CFTypeRef unused);
extern int64_t IOReportSimpleGetIntegerValue(CFDictionaryRef item, int32_t idx);
extern CFStringRef IOReportChannelGetGroup(CFDictionaryRef item);
extern CFStringRef IOReportChannelGetSubGroup(CFDictionaryRef item);
extern CFStringRef IOReportChannelGetChannelName(CFDictionaryRef item);
extern CFStringRef IOReportChannelGetUnitLabel(CFDictionaryRef item);
extern int32_t IOReportStateGetCount(CFDictionaryRef item);
extern CFStringRef IOReportStateGetNameForIndex(CFDictionaryRef item,
                                                int32_t idx);
extern int64_t IOReportStateGetResidency(CFDictionaryRef item, int32_t idx);

typedef void *IOHIDEventSystemClientRef;
typedef void *IOHIDServiceClientRef;
typedef void *IOHIDEventRef;

extern IOHIDEventSystemClientRef
IOHIDEventSystemClientCreate(CFAllocatorRef allocator);
extern int IOHIDEventSystemClientSetMatching(IOHIDEventSystemClientRef client,
                                             CFDictionaryRef matching);
extern CFArrayRef
IOHIDEventSystemClientCopyServices(IOHIDEventSystemClientRef client);
extern CFStringRef IOHIDServiceClientCopyProperty(IOHIDServiceClientRef service,
                                                  CFStringRef key);
extern IOHIDEventRef IOHIDServiceClientCopyEvent(IOHIDServiceClientRef service,
                                                 int64_t type, int32_t options,
                                                 int64_t timeout);
extern double IOHIDEventGetFloatValue(IOHIDEventRef event, int64_t field);

#define kHIDPage_AppleVendor 0xff00
#define kHIDUsage_AppleVendor_TemperatureSensor 0x0005
#define kIOHIDEventTypeTemperature 15

static IOReportSubscriptionRef g_subscription = NULL;
static CFMutableDictionaryRef g_channels = NULL;
static io_connect_t g_smcConn = 0;
static uint32_t g_gpu_freqs[64];
static int g_gpu_freq_count = 0;

static int cfStringStartsWith(CFStringRef str, const char *prefix);

static void loadGpuFrequencies() {
  if (g_gpu_freq_count > 0)
    return;

  io_iterator_t iterator;
  io_object_t entry;

  CFMutableDictionaryRef matching = IOServiceMatching("AppleARMIODevice");
  if (IOServiceGetMatchingServices(kIOMainPortDefault, matching, &iterator) !=
      kIOReturnSuccess)
    return;

  while ((entry = IOIteratorNext(iterator)) != 0) {
    io_name_t name;
    IORegistryEntryGetName(entry, name);

    if (strcmp(name, "pmgr") == 0 || strcmp(name, "clpc") == 0) {
      CFMutableDictionaryRef properties = NULL;
      if (IORegistryEntryCreateCFProperties(
              entry, &properties, kCFAllocatorDefault, 0) == kIOReturnSuccess) {

        CFIndex count = CFDictionaryGetCount(properties);
        const void *keys[count];
        const void *values[count];
        CFDictionaryGetKeysAndValues(properties, keys, values);

        CFDataRef bestData = NULL;
        uint32_t bestMaxFreq = 0xFFFFFFFF;
        int bestValidFreqs = 0;

        for (CFIndex i = 0; i < count; i++) {
          CFStringRef key = (CFStringRef)keys[i];
          char keyName[128];
          CFStringGetCString(key, keyName, sizeof(keyName),
                             kCFStringEncodingUTF8);

          if (strcmp(keyName, "voltage-states9-sram") == 0 ||
              strcmp(keyName, "voltage-states9") == 0) {
            bestData = (CFDataRef)values[i];
            break;
          }
        }

        if (bestData == NULL) {
          for (CFIndex i = 0; i < count; i++) {
            CFStringRef key = (CFStringRef)keys[i];
            if (cfStringStartsWith(key, "voltage-states")) {
              CFDataRef data = (CFDataRef)values[i];
              const uint8_t *bytes = CFDataGetBytePtr(data);
              CFIndex len = CFDataGetLength(data);
              int totalEntries = (int)(len / 8);

              int validFreqs = 0;
              uint32_t currentMaxFreq = 0;

              for (int j = 0; j < totalEntries; j++) {
                uint32_t val;
                memcpy(&val, bytes + (j * 8), 4);

                if (val > 100000000) {
                  validFreqs++;
                  if (val > currentMaxFreq) {
                    currentMaxFreq = val;
                  }
                }
              }

              if (validFreqs > 0) {
                if (currentMaxFreq < bestMaxFreq) {
                  bestMaxFreq = currentMaxFreq;
                  bestData = data;
                  bestValidFreqs = validFreqs;
                }
              }
            }
          }
        }

        if (bestData != NULL) {
          CFIndex len = CFDataGetLength(bestData);
          const uint8_t *bytes = CFDataGetBytePtr(bestData);
          int totalFreqs = (int)(len / 8);
          if (totalFreqs > 64)
            totalFreqs = 64;
          g_gpu_freq_count = 0;
          for (int i = 0; i < totalFreqs; i++) {
            uint32_t freq = 0;
            memcpy(&freq, bytes + (i * 8), 4);
            uint32_t freqMHz = freq / 1000000;
            if (freqMHz > 0) {
              g_gpu_freqs[g_gpu_freq_count++] = freqMHz;
            }
          }
        }
        CFRelease(properties);
      }
    }
    IOObjectRelease(entry);
  }
  IOObjectRelease(iterator);
}

int initIOReport() {
  if (g_channels != NULL) {
    return 0;
  }

  CFStringRef energyGroup = CFSTR("Energy Model");
  CFStringRef gpuGroup = CFSTR("GPU Stats");
  CFStringRef cpuGroup = CFSTR("CPU Stats");

  CFDictionaryRef energyChan =
      IOReportCopyChannelsInGroup(energyGroup, NULL, 0, 0, 0);
  CFDictionaryRef gpuChan =
      IOReportCopyChannelsInGroup(gpuGroup, NULL, 0, 0, 0);

  if (energyChan == NULL) {
    return -1;
  }

  if (gpuChan != NULL) {
    IOReportMergeChannels(energyChan, gpuChan, NULL);
    CFRelease(gpuChan);
  }

  CFIndex size = CFDictionaryGetCount(energyChan);
  g_channels =
      CFDictionaryCreateMutableCopy(kCFAllocatorDefault, size, energyChan);
  CFRelease(energyChan);

  if (g_channels == NULL) {
    return -2;
  }

  CFMutableDictionaryRef subsystem = NULL;
  g_subscription =
      IOReportCreateSubscription(NULL, g_channels, &subsystem, 0, NULL);

  if (g_subscription == NULL) {
    CFRelease(g_channels);
    g_channels = NULL;
    return -3;
  }

  loadGpuFrequencies();

  g_smcConn = SMCOpen();

  return 0;
}

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

static int cfStringMatch(CFStringRef str, const char *match) {
  if (str == NULL || match == NULL)
    return 0;
  CFStringRef matchStr = CFStringCreateWithCString(kCFAllocatorDefault, match,
                                                   kCFStringEncodingUTF8);
  if (matchStr == NULL)
    return 0;
  int result = (CFStringCompare(str, matchStr, 0) == kCFCompareEqualTo);
  CFRelease(matchStr);
  return result;
}

static int cfStringContains(CFStringRef str, const char *substr) {
  if (str == NULL || substr == NULL)
    return 0;
  CFStringRef substrRef = CFStringCreateWithCString(kCFAllocatorDefault, substr,
                                                    kCFStringEncodingUTF8);
  if (substrRef == NULL)
    return 0;
  CFRange result = CFStringFind(str, substrRef, 0);
  CFRelease(substrRef);
  return (result.location != kCFNotFound);
}

static int cfStringStartsWith(CFStringRef str, const char *prefix) {
  if (str == NULL || prefix == NULL)
    return 0;
  CFStringRef prefixRef = CFStringCreateWithCString(kCFAllocatorDefault, prefix,
                                                    kCFStringEncodingUTF8);
  if (prefixRef == NULL)
    return 0;
  int result = CFStringHasPrefix(str, prefixRef);
  CFRelease(prefixRef);
  return result;
}

static double energyToWatts(int64_t energy, CFStringRef unitRef,
                            double durationMs) {
  if (durationMs <= 0)
    durationMs = 1;
  double val = (double)energy;
  double rate = val / (durationMs / 1000.0);

  if (unitRef == NULL)
    return rate / 1e6;

  char unit[32] = {0};
  CFStringGetCString(unitRef, unit, sizeof(unit), kCFStringEncodingUTF8);

  for (int i = 0; unit[i]; i++) {
    if (unit[i] == ' ')
      unit[i] = '\0';
  }

  if (strcmp(unit, "mJ") == 0) {
    return rate / 1e3;
  } else if (strcmp(unit, "uJ") == 0) {
    return rate / 1e6;
  } else if (strcmp(unit, "nJ") == 0) {
    return rate / 1e9;
  }
  return rate / 1e6;
}

static float readSocTemperature() {
  const void *keys[2] = {CFSTR("PrimaryUsagePage"), CFSTR("PrimaryUsage")};
  int page = kHIDPage_AppleVendor;
  int usage = kHIDUsage_AppleVendor_TemperatureSensor;
  CFNumberRef pageNum =
      CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &page);
  CFNumberRef usageNum =
      CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &usage);
  const void *values[2] = {pageNum, usageNum};

  CFDictionaryRef matching = CFDictionaryCreate(
      kCFAllocatorDefault, keys, values, 2, &kCFTypeDictionaryKeyCallBacks,
      &kCFTypeDictionaryValueCallBacks);
  CFRelease(pageNum);
  CFRelease(usageNum);

  IOHIDEventSystemClientRef client =
      IOHIDEventSystemClientCreate(kCFAllocatorDefault);
  if (client == NULL) {
    CFRelease(matching);
    return 0;
  }

  IOHIDEventSystemClientSetMatching(client, matching);
  CFRelease(matching);

  CFArrayRef services = IOHIDEventSystemClientCopyServices(client);
  if (services == NULL) {
    CFRelease(client);
    return 0;
  }

  float tempSum = 0;
  int tempCount = 0;

  CFIndex count = CFArrayGetCount(services);
  for (CFIndex i = 0; i < count; i++) {
    IOHIDServiceClientRef service =
        (IOHIDServiceClientRef)CFArrayGetValueAtIndex(services, i);
    if (service == NULL)
      continue;

    CFStringRef productRef =
        IOHIDServiceClientCopyProperty(service, CFSTR("Product"));
    if (productRef == NULL)
      continue;

    char product[128] = {0};
    CFStringGetCString(productRef, product, sizeof(product),
                       kCFStringEncodingUTF8);

    IOHIDEventRef event =
        IOHIDServiceClientCopyEvent(service, kIOHIDEventTypeTemperature, 0, 0);
    if (event == NULL) {
      CFRelease(productRef);
      continue;
    }

    double temp =
        IOHIDEventGetFloatValue(event, kIOHIDEventTypeTemperature << 16);
    CFRelease(event);
    CFRelease(productRef);

    if (temp > 0 && temp < 150) {
      if (strstr(product, "PMU tdie") != NULL ||
          strstr(product, "pACC") != NULL || strstr(product, "eACC") != NULL ||
          strstr(product, "GPU") != NULL) {
        tempSum += temp;
        tempCount++;
      }
    }
  }

  CFRelease(services);
  CFRelease(client);

  return (tempCount > 0) ? (tempSum / tempCount) : 0;
}

PowerMetrics samplePowerMetrics(int durationMs) {
  PowerMetrics metrics = {0, 0, 0, 0, 0, 0, 0, 0, 0};

  if (g_subscription == NULL || g_channels == NULL) {
    if (initIOReport() != 0) {
      return metrics;
    }
  }

  CFDictionaryRef sample1 =
      IOReportCreateSamples(g_subscription, g_channels, NULL);

  if (sample1 == NULL)
    return metrics;

  usleep(durationMs * 1000);

  CFDictionaryRef sample2 =
      IOReportCreateSamples(g_subscription, g_channels, NULL);

  if (sample2 == NULL) {
    CFRelease(sample1);
    return metrics;
  }

  CFDictionaryRef delta = IOReportCreateSamplesDelta(sample1, sample2, NULL);
  CFRelease(sample1);
  CFRelease(sample2);

  if (delta == NULL)
    return metrics;

  CFArrayRef channels = CFDictionaryGetValue(delta, CFSTR("IOReportChannels"));
  if (channels == NULL) {
    CFRelease(delta);
    return metrics;
  }

  CFIndex count = CFArrayGetCount(channels);
  for (CFIndex i = 0; i < count; i++) {
    CFDictionaryRef item = (CFDictionaryRef)CFArrayGetValueAtIndex(channels, i);
    if (item == NULL)
      continue;

    CFStringRef groupRef = IOReportChannelGetGroup(item);
    CFStringRef channelRef = IOReportChannelGetChannelName(item);

    if (groupRef == NULL || channelRef == NULL)
      continue;

    if (cfStringMatch(groupRef, "Energy Model")) {
      CFStringRef unitRef = IOReportChannelGetUnitLabel(item);
      int64_t val = IOReportSimpleGetIntegerValue(item, 0);
      double watts = energyToWatts(val, unitRef, (double)durationMs);

      if (cfStringContains(channelRef, "CPU Energy")) {
        metrics.cpuPower += watts;
      } else if (cfStringMatch(channelRef, "GPU Energy")) {
        metrics.gpuPower += watts;
      } else if (cfStringStartsWith(channelRef, "ANE")) {
        metrics.anePower += watts;
      } else if (cfStringStartsWith(channelRef, "DRAM")) {
        metrics.dramPower += watts;
      } else if (cfStringStartsWith(channelRef, "GPU SRAM")) {
        metrics.gpuSramPower += watts;
      }
    } else if (cfStringMatch(groupRef, "GPU Stats")) {
      CFStringRef subgroupRef = IOReportChannelGetSubGroup(item);
      if (subgroupRef != NULL &&
          cfStringMatch(subgroupRef, "GPU Performance States")) {
        if (cfStringMatch(channelRef, "GPUPH")) {
          int32_t stateCount = IOReportStateGetCount(item);
          int64_t totalTime = 0;
          int64_t activeTime = 0;
          double weightedFreq = 0;
          int activeStateIdx = 0;

          for (int32_t s = 0; s < stateCount; s++) {
            int64_t residency = IOReportStateGetResidency(item, s);
            CFStringRef stateName = IOReportStateGetNameForIndex(item, s);
            totalTime += residency;

            if (stateName != NULL && !cfStringMatch(stateName, "OFF") &&
                !cfStringMatch(stateName, "IDLE") &&
                !cfStringMatch(stateName, "DOWN")) {
              activeTime += residency;
              if (g_gpu_freq_count > 0 && activeStateIdx < g_gpu_freq_count) {
                weightedFreq += (double)g_gpu_freqs[activeStateIdx] * residency;
              }
              activeStateIdx++;
            }
          }

          if (totalTime > 0) {
            metrics.gpuActive = (double)activeTime / (double)totalTime * 100.0;
          }
          if (activeTime > 0 && g_gpu_freq_count > 0) {
            metrics.gpuFreqMHz = (int)(weightedFreq / activeTime);
          }
        }
      }
    }
  }

  metrics.socTemp = readSocTemperature();

  if (g_smcConn) {
    metrics.systemPower = SMCGetFloatValue(g_smcConn, "PSTR");
  }

  CFRelease(delta);

  return metrics;
}

void cleanupIOReport() {
  if (g_channels != NULL) {
    CFRelease(g_channels);
    g_channels = NULL;
  }
  g_subscription = NULL;
  if (g_smcConn) {
    SMCClose(g_smcConn);
    g_smcConn = 0;
  }
}

int getThermalState() {
  NSProcessInfo *info = [NSProcessInfo processInfo];
  return (int)[info thermalState];
}
