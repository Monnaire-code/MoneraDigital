import { formatNumber } from '@/lib/utils';

/**
 * Calculate the number of days remaining until the given end date
 * @param endDate End date (string or Date object)
 * @returns Number of days remaining (0 if date is in the past or today)
 */
export const getDaysRemaining = (endDate: string | Date): number => {
  if (!endDate) return 0;

  const end = new Date(endDate);

  // Check if it's a valid date
  if (isNaN(end.getTime())) return 0;

  const now = new Date();
  const diff = end.getTime() - now.getTime();
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24));
  return days > 0 ? days : 0;
};

/**
 * Result of interest calculation
 */
export interface InterestCalculationResult {
  interest: number;
  principal: number;
  rate: number;
  days: number;
  dailyRate: number;
  totalAmount: number;
}

/**
 * Result of date calculation
 */
export interface DateCalculationResult {
  startDate: Date;
  endDate: Date;
  actualDays: number;
  calendarDays: number;
}

/**
 * Calculate deposit dates based on duration and start mode
 * @param duration Duration in days
 * @param startMode Start mode: 'today', 'tomorrow', 'business_day'
 * @returns Date calculation result
 */
export const calculateDepositDates = (
  duration: number,
  startMode: 'today' | 'tomorrow' | 'business_day' = 'tomorrow'
): DateCalculationResult => {
  const now = new Date();
  const result: DateCalculationResult = {
    startDate: new Date(),
    endDate: new Date(),
    actualDays: duration,
    calendarDays: duration
  };

  // Set start date based on mode
  switch (startMode) {
    case 'today':
      result.startDate.setHours(0, 0, 0, 0);
      break;
    case 'tomorrow':
      result.startDate.setDate(result.startDate.getDate() + 1);
      result.startDate.setHours(0, 0, 0, 0);
      break;
    case 'business_day':
      // Find next business day
      result.startDate.setDate(result.startDate.getDate() + 1);
      result.startDate.setHours(0, 0, 0, 0);
      while (result.startDate.getDay() === 0 || result.startDate.getDay() === 6) {
        result.startDate.setDate(result.startDate.getDate() + 1);
      }
      break;
  }

  // Calculate end date
  result.endDate = new Date(result.startDate);
  result.endDate.setDate(result.endDate.getDate() + duration);

  return result;
};

/**
 * Validate subscription amount against limits and balance
 * @param amount Amount to validate
 * @param minAmount Minimum allowed amount
 * @param maxAmount Maximum allowed amount
 * @param availableBalance Available balance
 * @param t Translation function for i18n (optional)
 * @returns Validation result
 */
export const validateAmount = (
  amount: number | string,
  minAmount: number,
  maxAmount: number,
  availableBalance: number,
  t?: (key: string) => string
): { valid: boolean; error?: string } => {
  const amountNum = typeof amount === 'string' ? parseFloat(amount) : amount;

  if (isNaN(amountNum) || amountNum <= 0) {
    return { valid: false, error: t ? t("auth.errors.wealth.invalidAmount") : 'Invalid amount' };
  }

  if (amountNum < minAmount) {
    const formattedMin = formatNumber(minAmount);
    return {
      valid: false,
      error: t ? `${t("dashboard.fixedDeposit.amountBelowMinError")} ${formattedMin}` : `Amount below minimum (${formattedMin})`
    };
  }

  if (amountNum > maxAmount) {
    const formattedMax = formatNumber(maxAmount);
    return {
      valid: false,
      error: t ? `${t("dashboard.fixedDeposit.amountAboveMaxError")} ${formattedMax}` : `Amount above maximum (${formattedMax})`
    };
  }

  if (amountNum > availableBalance) {
    return { valid: false, error: t ? t("auth.errors.wealth.insufficientBalance") : 'Insufficient balance' };
  }

  return { valid: true };
};

/**
 * Generate unique request ID for concurrent request handling
 * @returns Unique request identifier
 */
export const generateRequestId = (): string => {
  const timestamp = Date.now();
  const random = Math.random().toString(36).substr(2, 9);
  return `request_${timestamp}_${random}`;
};

/**
 * Check if two dates are on the same business day
 * @param date1 First date
 * @param date2 Second date
 * @returns True if same business day
 */
export const isSameBusinessDay = (date1: Date, date2: Date): boolean => {
  const d1 = new Date(date1);
  const d2 = new Date(date2);

  // Reset time components
  d1.setHours(0, 0, 0, 0);
  d2.setHours(0, 0, 0, 0);

  // Check if same day
  if (d1.getTime() !== d2.getTime()) return false;

  // Check if weekend
  const dayOfWeek = d1.getDay();
  return dayOfWeek >= 1 && dayOfWeek <= 5;
};

/**
 * Calculate compound interest using ACT/365 day count convention
 * @param principal Principal amount
 * @param rate Annual rate (as decimal)
 * @param days Number of days
 * @param dayCount Day count convention ('act_365', 'act_360', '30_360')
 * @returns Compound interest amount
 */
export const calculateInterest = (
  principal: number,
  rate: number,
  days: number,
  dayCount: 'act_365' | 'act_360' | '30_360' = 'act_365'
): { interest: number; principal: number; rate: number; days: number; dailyRate: number; totalAmount: number } => {
  let dailyRate: number;

  switch (dayCount) {
    case 'act_365':
      dailyRate = rate / 365;
      break;
    case 'act_360':
      dailyRate = rate / 360;
      break;
    case '30_360':
      dailyRate = rate / 360;
      break;
    default:
      dailyRate = rate / 365;
  }

  const interest = principal * dailyRate * days;
  const totalAmount = principal + interest;

  return {
    interest,
    principal,
    rate,
    days,
    dailyRate,
    totalAmount
  };
};

/**
 * Format currency amount with appropriate precision
 * @param amount Amount to format
 * @param currency Currency code (default: 'USDT')
 * @param precision Decimal places (auto-detect based on currency)
 * @returns Formatted amount string
 */
export const formatCurrency = (
  amount: number | string,
  currency: string = 'USDT',
  precision?: number
): string => {
  const num = typeof amount === 'string' ? parseFloat(amount) : amount;
  
  if (isNaN(num)) return '0';

  // Auto-detect precision based on currency
  if (precision === undefined) {
    switch (currency.toUpperCase()) {
      case 'BTC':
        precision = 8;
        break;
      case 'ETH':
        precision = 6;
        break;
      case 'USDT':
      case 'USDC':
        precision = 2;
        break;
      default:
        precision = 2;
    }
  }

  return num.toFixed(precision);
};

// Re-export functions for testing
export { formatNumber } from '@/lib/utils';