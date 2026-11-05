import type { RecentRequestBucket } from '@/utils/recentRequests';

/** 每个统计桶覆盖的分钟数（后端固定为 10 分钟 × 20 桶） */
export const TRAFFIC_BUCKET_MINUTES = 10;

/** 聚合后的整体流量窗口 */
export interface TrafficWindow {
  buckets: RecentRequestBucket[];
  totalSuccess: number;
  totalFailure: number;
  total: number;
  /** 0–100；窗口内无请求时为 null */
  successRate: number | null;
  /** 单桶最大请求数，用于图表纵轴 */
  peakTotal: number;
  /** 峰值所在桶下标，-1 表示无数据 */
  peakIndex: number;
  /** 窗口跨度（分钟） */
  windowMinutes: number;
}

/** 顶部计数卡片的原始数值 */
export interface DashboardCounts {
  managementKeys: number | null;
  providerKeys: number | null;
  credentials: number | null;
  models: number | null;
}
