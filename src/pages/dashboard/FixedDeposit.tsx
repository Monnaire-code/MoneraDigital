import { useState, useEffect, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/ui/dialog";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { PiggyBank, TrendingUp, Info, ArrowRight, Clock, Percent, ShieldCheck, RefreshCw, History, AlertTriangle, Wallet } from "lucide-react";
import { toast } from "sonner";
import {
  calculateInterest,
  calculateDepositDates,
  getDaysRemaining,
  validateAmount,
  generateRequestId,
  InterestCalculationResult
} from "@/lib/wealth-utils";
import { getAccessToken } from "@/lib/api-client";

interface Product {
  id: number;
  name: string;
  currency: string;
  apy: number;
  duration: number;
  minAmount: number;
  maxAmount: number;
  totalQuota: number;
  autoRenew: boolean;
}

interface WealthOrder {
  id: number;
  productTitle: string;
  currency: string;
  amount: string;
  interestExpected: string;
  interestPaid: string;
  interestAccrued: string;
  startDate: string;
  endDate: string;
  autoRenew: boolean;
  status: number;
  duration?: number; // 产品期限（天数）
}

interface Asset {
  currency: string;
  total: string;
  available: string;
}

interface PaginationProps {
  currentPage: number;
  totalPages: number;
  onPageChange: (page: number) => void;
}

const Pagination = ({ currentPage, totalPages, onPageChange }: PaginationProps) => {
  if (totalPages <= 1) return null;

  return (
    <div className="flex items-center justify-center gap-2 mt-4">
      <Button
        variant="outline"
        size="sm"
        onClick={() => onPageChange(currentPage - 1)}
        disabled={currentPage <= 1}
      >
        <ChevronLeft size={16} />
      </Button>
      <span className="text-sm text-muted-foreground px-2">
        {currentPage} / {totalPages}
      </span>
      <Button
        variant="outline"
        size="sm"
        onClick={() => onPageChange(currentPage + 1)}
        disabled={currentPage >= totalPages}
      >
        <ChevronRight size={16} />
      </Button>
    </div>
  );
};

const FixedDeposit = () => {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [selectedProduct, setSelectedProduct] = useState<Product | null>(null);
  const [amount, setAmount] = useState("");
  const [autoRenew, setAutoRenew] = useState(false);
  const [activeTab, setActiveTab] = useState("products");

  const [orders, setOrders] = useState<WealthOrder[]>([]);
  const [products, setProducts] = useState<Product[]>([]);
  const [selectedOrder, setSelectedOrder] = useState<WealthOrder | null>(null);
  const [redeemDialogOpen, setRedeemDialogOpen] = useState(false);
  const [redeemAmount, setRedeemAmount] = useState("");
  const [isRedeeming, setIsRedeeming] = useState(false);
  const [showConfirmDialog, setShowConfirmDialog] = useState(false);
  const [isSubscribing, setIsSubscribing] = useState(false);
  const [concurrentAttempts, setConcurrentAttempts] = useState(0);
  const [retryCount, setRetryCount] = useState(0);
  const [networkTimeout, setNetworkTimeout] = useState(false);
  const [operationProgress, setOperationProgress] = useState<string>("idle");
  const [loadingStates, setLoadingStates] = useState({
    assets: false,
    products: false,
    orders: false,
    subscribe: false
  });
  const timeoutRef = useRef<NodeJS.Timeout | null>(null);

  const [assets, setAssets] = useState<Asset[]>([]);
  const [isLoadingAssets, setIsLoadingAssets] = useState(true);

  // 统一的币种筛选器
  const [currencyFilter, setCurrencyFilter] = useState<string | null>(null);

  const [productsPage, setProductsPage] = useState(1);
  const [productsTotal, setProductsTotal] = useState(0);
  const [ordersPage, setOrdersPage] = useState(1);
  const [ordersTotal, setOrdersTotal] = useState(0);

  const fetchAssets = async () => {
    try {
      setLoadingStates(prev => ({ ...prev, assets: true }));
      
      const token = localStorage.getItem("token");
      if (!token) {
        navigate("/login");
        return;
      }
      const res = await fetch("/api/assets", {
        headers: { Authorization: `Bearer ${token}` }
      });
      if (res.status === 401 || res.status === 403) {
        localStorage.removeItem("token");
        localStorage.removeItem("user");
        navigate("/login");
        return;
      }
      if (res.ok) {
        const data = await res.json();
        setAssets(Array.isArray(data.assets) ? data.assets : []);
      }
    } catch (error) {
      console.error("Failed to fetch assets", error);
      toast.error(t("auth.errors.networkError"));
    } finally {
      setIsLoadingAssets(false);
      setLoadingStates(prev => ({ ...prev, assets: false }));
    }
  };

  const fetchOrders = async (currency?: string) => {
    try {
      setLoadingStates(prev => ({ ...prev, orders: true }));
      
      const token = localStorage.getItem("token");
      if (!token) {
        navigate("/login");
        return;
      }
      const url = currency 
        ? `/api/wealth/orders?currency=${currency}&page=${ordersPage}&pageSize=20`
        : `/api/wealth/orders?page=${ordersPage}&pageSize=20`;
      const res = await fetch(url, {
        headers: { Authorization: `Bearer ${token}` }
      });
      if (res.status === 401 || res.status === 403) {
        localStorage.removeItem("token");
        localStorage.removeItem("user");
        navigate("/login");
        return;
      }
      if (res.ok) {
        const data = await res.json();
        if (Array.isArray(data.orders)) {
          // 如果后端没有返回duration，尝试从产品信息中获取，并增强数据处理
          const ordersWithDuration = data.orders.map((order: any) => {
            // 确保duration字段存在，如果没有则从product中获取或计算
            const duration = order.duration || order.product?.duration || order.productDuration;

            // 确保时间字段存在并格式化
            const startDate = order.start_date || order.startDate || order.created_at;
            const endDate = order.end_date || order.endDate;

            return {
              ...order,
              duration,
              startDate,
              endDate,
              // 确保其他关键字段也存在
              productTitle: order.productTitle || order.product_title || order.productName || order.product?.name || '未知产品',
              amount: order.amount || order.investAmount || '0',
              currency: order.currency || 'USDT',
              interestExpected: order.interestExpected || order.expectedInterest || order.interest_expected || '0',
              interestPaid: order.interestPaid || order.paidInterest || '0',
              interestAccrued: order.interestAccrued || order.accruedInterest || '0',
              status: order.status || 0,
              autoRenew: order.autoRenew || order.auto_renew || false
            };
          });
          setOrders(ordersWithDuration);
          setOrdersTotal(data.total || 0);
        } else if (Array.isArray(data)) {
          const ordersWithDuration = data.map((order: any) => {
            // 确保duration字段存在
            const duration = order.duration || order.product?.duration || order.productDuration;

            // 确保时间字段存在并格式化
            const startDate = order.start_date || order.startDate || order.created_at;
            const endDate = order.end_date || order.endDate;

            return {
              ...order,
              duration,
              startDate,
              endDate,
              // 确保其他关键字段也存在
              productTitle: order.productTitle || order.product_title || order.productName || order.product?.name || '未知产品',
              amount: order.amount || order.investAmount || '0',
              currency: order.currency || 'USDT',
              interestExpected: order.interestExpected || order.expectedInterest || order.interest_expected || '0',
              interestPaid: order.interestPaid || order.paidInterest || '0',
              interestAccrued: order.interestAccrued || order.accruedInterest || '0',
              status: order.status || 0,
              autoRenew: order.autoRenew || order.auto_renew || false
            };
          });
          setOrders(ordersWithDuration);
          setOrdersTotal(data.length);
        }
      }
    } catch (error) {
      console.error("Failed to fetch orders", error);
      toast.error(t("auth.errors.networkError"));
    } finally {
      setLoadingStates(prev => ({ ...prev, orders: false }));
    }
  };

  const fetchProducts = async (currency?: string) => {
    try {
      setLoadingStates(prev => ({ ...prev, products: true }));
      
      const token = localStorage.getItem("token");
      if (!token) {
        navigate("/login");
        return;
      }
      const url = currency 
        ? `/api/wealth/products?currency=${currency}&page=${productsPage}&pageSize=10`
        : `/api/wealth/products?page=${productsPage}&pageSize=10`;
      const res = await fetch(url, {
        headers: { Authorization: `Bearer ${token}` }
      });
      if (res.status === 401 || res.status === 403) {
        localStorage.removeItem("token");
        localStorage.removeItem("user");
        navigate("/login");
        return;
      }
      if (res.ok) {
        const data = await res.json();
        if (Array.isArray(data.products)) {
          const transformed = data.products.map((p: any) => ({
            id: p.id,
            name: p.title,
            currency: p.currency,
            apy: p.apy,
            duration: p.duration,
            minAmount: parseFloat(p.minAmount),
            maxAmount: parseFloat(p.maxAmount),
            totalQuota: parseFloat(p.remainingQuota),
            autoRenew: p.autoRenewAllowed
          }));
          setProducts(transformed);
          setProductsTotal(data.total || 0);
        } else if (Array.isArray(data)) {
          const transformed = data.map((p: any) => ({
            id: p.id,
            name: p.title,
            currency: p.currency,
            apy: p.apy,
            duration: p.duration,
            minAmount: parseFloat(p.minAmount),
            maxAmount: parseFloat(p.maxAmount),
            totalQuota: parseFloat(p.remainingQuota),
            autoRenew: p.autoRenewAllowed
          }));
          setProducts(transformed);
          setProductsTotal(data.length);
        }
      }
    } catch (error) {
      console.error("Failed to fetch products", error);
    } finally {
      setLoadingStates(prev => ({ ...prev, products: false }));
    }
  };

  useEffect(() => {
    fetchAssets();
  }, []);

  // 根据当前Tab和币种筛选获取相应数据
  useEffect(() => {
    if (assets.length > 0 && currencyFilter) {
      if (activeTab === "products") {
        fetchProducts(currencyFilter);
      } else if (activeTab === "history") {
        fetchOrders(currencyFilter);
      }
    } else if (assets.length > 0) {
      setProducts([]);
      setProductsTotal(0);
      setOrders([]);
      setOrdersTotal(0);
    }
  }, [assets.length, currencyFilter, activeTab]);

  // 当Tab切换时获取对应数据
  useEffect(() => {
    if (currencyFilter && assets.length > 0) {
      if (activeTab === "products") {
        fetchProducts(currencyFilter);
      } else if (activeTab === "history") {
        fetchOrders(currencyFilter);
      }
    }
  }, [activeTab, currencyFilter, assets.length]);

  const displayAssets = assets || [];
  const displayProducts = products || [];
  const displayOrders = orders || [];

  const allCurrencies = useMemo(() => {
    const productCurrencies = displayProducts.map(p => p.currency);
    const orderCurrencies = displayOrders.map(o => o.currency);
    const assetCurrencies = displayAssets.map(a => a.currency);
    return [...new Set([...productCurrencies, ...orderCurrencies, ...assetCurrencies])].sort();
  }, [displayAssets, displayOrders, displayProducts]);

  useEffect(() => {
    // 只在初始化时设置默认的币种筛选器
    if (allCurrencies.length > 0 && !currencyFilter) {
      setCurrencyFilter(allCurrencies[0]);
    }
  }, [allCurrencies, currencyFilter]);

  const filteredProducts = useMemo(() => {
    if (!currencyFilter) return displayProducts;
    return displayProducts.filter(p => p.currency === currencyFilter);
  }, [displayProducts, currencyFilter]);

  const filteredOrders = useMemo(() => {
    if (!currencyFilter) return displayOrders;
    return displayOrders.filter(o => o.currency === currencyFilter);
  }, [displayOrders, currencyFilter]);

  const handleSubscribe = async () => {
    if (!selectedProduct || !amount) return;

    // 防止并发申购 - 客户端级别
    if (isSubscribing) {
      toast.error(t("dashboard.fixedDeposit.alreadySubscribing"));
      return;
    }

    // 检查并发请求次数
    if (concurrentAttempts > 3) {
      toast.error(t("dashboard.fixedDeposit.tooManyAttempts"));
      return;
    }

    setIsSubscribing(true);
    setConcurrentAttempts(prev => prev + 1);

    // 设置网络超时监控
    const requestTimeout = 30000; // 30秒超时
    timeoutRef.current = setTimeout(() => {
      setNetworkTimeout(true);
      toast.error(t("dashboard.fixedDeposit.requestTimeoutError"), {
        duration: 8000,
        description: "Network is slow. You can retry the operation."
      });
    }, requestTimeout);

    try {
      const token = localStorage.getItem("token");
      if (!token) {
        navigate("/login");
        return;
      }

      // 并发控制：生成请求ID
      const requestId = generateRequestId();

      // 计算预期利息 - 使用高精度计算
      const interestCalculation = getInterestCalculation();

      const subscribeData = {
        // 产品信息
        productId: selectedProduct.id,
        productName: selectedProduct.name,
        currency: selectedProduct.currency,
        apy: selectedProduct.apy,
        duration: selectedProduct.duration,
        // 用户输入
        amount: amount,
        autoRenew: autoRenew,
        // 预期利息计算 - 使用正确的数据库字段名
        interest_expected: interestCalculation.interest,
        // 调试信息
        debug_interest: {
          calculated: interestCalculation.interest,
          principal: interestCalculation.principal,
          dailyRate: interestCalculation.dailyRate,
          totalAmount: interestCalculation.totalAmount
        },
        // 并发控制
        requestId: requestId,
        clientTimestamp: Date.now(),
        userAgent: navigator.userAgent,
        retryCount: retryCount,
        // 安全验证
        ip: '', // 将在服务器端获取
        sessionId: localStorage.getItem('sessionId'),
      };

      // 日志记录申购数据
      console.log('申购请求数据:', JSON.stringify(subscribeData, null, 2));

      const res = await fetchWithRetry("/api/wealth/subscribe", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": requestId,
          "X-Client-Timestamp": subscribeData.clientTimestamp.toString(),
          Authorization: `Bearer ${token}`
        },
        body: JSON.stringify(subscribeData)
      }, retryCount);

      // 清除超时定时器
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
        setNetworkTimeout(false);
      }

      if (res.status === 401 || res.status === 403) {
        localStorage.removeItem("token");
        localStorage.removeItem("user");
        navigate("/login");
        return;
      }

      if (!res.ok) {
        const errorData = await res.json().catch(() => ({}));
        
        // 检查是否是幂等性相关错误
        if (res.status === 400 && errorData.code === 'MISSING_IDEMPOTENCY_KEY') {
          toast.error(t("dashboard.fixedDeposit.idempotencyError"), {
            duration: 8000,
            description: t("dashboard.fixedDeposit.idempotencyKeyMissing")
          });
          return;
        }
        
        if (res.status === 503 && errorData.code === 'IDEMPOTENCY_UNAVAILABLE') {
          toast.error(t("dashboard.fixedDeposit.idempotencyUnavailable"), {
            duration: 8000,
            description: t("dashboard.fixedDeposit.idempotencyUnavailableDesc")
          });
          return;
        }
        
        if (res.status === 503 && errorData.code === 'IDEMPOTENCY_CHECK_FAILED') {
          toast.error(t("dashboard.fixedDeposit.idempotencyCheckFailed"), {
            duration: 8000,
            description: t("dashboard.fixedDeposit.idempotencyCheckFailedDesc")
          });
          return;
        }
        
        // 检查是否是并发请求冲突
        if (res.status === 409 || errorData.error?.includes('concurrent') || errorData.error?.includes('duplicate')) {
          throw new Error(t("dashboard.fixedDeposit.concurrentRequestError"));
        }
        
        // 检查是否是额度已满
        if (res.status === 400 && (errorData.error?.includes('quota') || errorData.error?.includes('full'))) {
          throw new Error(t("dashboard.fixedDeposit.quotaFull"));
        }

        const friendlyError = getFriendlyErrorMessage(errorData.error, t);
        throw new Error(friendlyError);
      }

      const data = await res.json();
      
      // 日志记录申购响应数据
      console.log('申购响应数据:', JSON.stringify(data, null, 2));
      
      // 申购成功
      setShowConfirmDialog(false);
      setOpen(false);
      toast.success(t("dashboard.fixedDeposit.subscribeSuccess"));
      
      // 显示申购详情
      if (data.orderId) {
        toast.info(`订单号: #${data.orderId.toString().padStart(6, '0')}`);
      }
      
      // 显示预期利息 - 优先使用后端返回的值，其次使用本地计算的值
      const calculatedInterest = data.interest_expected || data.interestExpected || interestCalculation.interest;
      console.log('申购响应利息字段分析:', {
        backendInterestExpected: data.interestExpected,
        backendInterest_expected: data.interest_expected,
        localCalculation: interestCalculation.interest,
        finalInterest: calculatedInterest
      });
      if (calculatedInterest) {
        toast.info(`${t("dashboard.fixedDeposit.interest")}: ${formatNumber(calculatedInterest)} ${selectedProduct.currency}`);
      }

      // 记录完整的申购交易信息
      console.log('申购交易详情:', {
        orderId: data.orderId,
        productName: selectedProduct.name,
        amount: amount,
        currency: selectedProduct.currency,
        apy: selectedProduct.apy,
        duration: selectedProduct.duration,
        interestExpected: calculatedInterest,
        orderStatus: data.status,
        startDate: data.startDate,
        endDate: data.endDate,
        autoRenew: autoRenew,
        transactionTime: new Date().toISOString(),
        retriesUsed: retryCount,
        backendResponse: data // 完整的后端响应
      });

      // 申购成功的详细提示
      const successMessage = t("dashboard.fixedDeposit.subscribeSuccess");
      toast.success(successMessage, {
        duration: 5000,
        description: `产品: ${selectedProduct?.name} | 金额: ${formatNumber(amount)} ${selectedProduct?.currency}`
      });

      // 清理表单
      setSelectedProduct(null);
      setAmount("");
      setAutoRenew(false);

      // 刷新数据
      await Promise.all([
        fetchAssets(),
        fetchOrders(currencyFilter || undefined)
      ]);

    } catch (error: unknown) {
      // 清除超时定时器
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
      }
      
      const errorMessage = error instanceof Error ? error.message : t("dashboard.fixedDeposit.subscribeFailed");
      
      // 智能重试逻辑 - 针对特定错误类型
      if (shouldRetry(errorMessage) && retryCount < 2) {
        setRetryCount(prev => prev + 1);
        toast.warning("Retrying operation...", {
          duration: 2000
        });
        
        // 延迟重试
        await new Promise(resolve => setTimeout(resolve, 1000 * (retryCount + 1)));
        return handleSubscribe(); // 递归重试
      }
      
      // 如果是系统繁忙或并发问题，提供更友好的提示
      if (errorMessage.includes(t("dashboard.fixedDeposit.concurrentRequestError")) || 
          errorMessage.includes(t("dashboard.fixedDeposit.systemBusyError"))) {
        toast.error(errorMessage, {
          duration: 8000,
          description: t("dashboard.fixedDeposit.retryLater")
        });
      } else if (networkTimeout) {
        toast.error(t("dashboard.fixedDeposit.connectionTimeout"), {
          duration: 8000
        });
      } else {
        toast.error(errorMessage, {
          duration: 6000
        });
      }
    } finally {
      setIsSubscribing(false);
      setConcurrentAttempts(0);
      setRetryCount(0);
      setNetworkTimeout(false);
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current);
      }
    }
  };

  const shouldRetry = (errorMessage: string): boolean => {
    // Only retry for network-related or temporary errors
    const retryableErrors = [
      'network',
      'timeout',
      'concurrent',
      'duplicate',
      'system busy',
      '503',
      '502',
      '504'
    ];
    
    return retryableErrors.some(error => 
      errorMessage.toLowerCase().includes(error.toLowerCase())
    );
  };

  const fetchWithRetry = async (url: string, options: RequestInit, retries: number): Promise<Response> => {
    const maxRetries = 3;
    let lastError: Error | null = null;
    
    for (let i = 0; i <= maxRetries; i++) {
      try {
        const controller = new AbortController();
        const timeoutId = setTimeout(() => controller.abort(), 25000); // 25秒内部超时
        
        const response = await fetch(url, {
          ...options,
          signal: controller.signal
        });
        
        clearTimeout(timeoutId);
        return response;
        
      } catch (error) {
        lastError = error instanceof Error ? error : new Error('Unknown error');
        
        if (i === maxRetries) {
          throw lastError;
        }
        
        // 指数退避策略
        const delay = Math.pow(2, i) * 500; // 500ms, 1s, 2s, 4s
        await new Promise(resolve => setTimeout(resolve, delay));
      }
    }
    
    throw lastError || new Error('Max retries exceeded');
  };

  const getFriendlyErrorMessage = (errorKey: string, t: any): string => {
    const errorMap: Record<string, string> = {
      "insufficient balance": t("auth.errors.wealth.insufficientBalance"),
      "product not found": t("auth.errors.wealth.productNotFound"),
      "order not found": t("auth.errors.wealth.orderNotFound"),
      "product not available": t("auth.errors.wealth.productNotAvailable"),
      "amount below minimum": t("auth.errors.wealth.amountBelowMin"),
      "amount above maximum": t("auth.errors.wealth.amountAboveMax"),
      "quota exceeded": t("auth.errors.wealth.quotaExceeded"),
      "order already redeemed": t("auth.errors.wealth.orderAlreadyRedeemed"),
      "invalid redemption type": t("auth.errors.wealth.invalidRedemptionType"),
      "failed to fetch price": t("auth.errors.wealth.priceFetchFailed"),
      "product is not active": t("auth.errors.wealth.productNotAvailable"),
      "quota full": t("auth.errors.wealth.quotaFull"),
      "minimum subscription": t("auth.errors.wealth.minimumSubscription"),
      "maximum subscription": t("auth.errors.wealth.maximumSubscription"),
      "invalid amount": t("auth.errors.wealth.invalidAmount"),
      "concurrent request": t("dashboard.fixedDeposit.concurrentRequestError"),
      "duplicate request": t("dashboard.fixedDeposit.concurrentRequestError"),
      "request timeout": t("dashboard.fixedDeposit.requestTimeoutError"),
      "system busy": t("dashboard.fixedDeposit.systemBusyError"),
    };
    return errorMap[errorKey] || errorKey || t("auth.errors.unknownError");
  };

  const handleRedeem = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedOrder) return;

    setIsRedeeming(true);
    try {
      const token = getAccessToken();
      if (!token) {
        navigate("/login");
        return;
      }
      const res = await fetch("/api/wealth/redeem", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`
        },
        body: JSON.stringify({
          orderId: selectedOrder.id,
          redemptionType: "full"
        })
      });

      if (res.status === 401 || res.status === 403) {
        localStorage.removeItem("token");
        localStorage.removeItem("user");
        navigate("/login");
        return;
      }

      if (!res.ok) {
        const errorData = await res.json().catch(() => ({}));
        const friendlyError = getFriendlyErrorMessage(errorData.error, t);
        throw new Error(friendlyError);
      }

      toast.success(t("dashboard.fixedDepositHistory.redeemSuccess"));
      setRedeemDialogOpen(false);
      setRedeemAmount("");
      fetchOrders(currencyFilter || undefined);
      fetchAssets();
    } catch (error: unknown) {
      const errorMessage = error instanceof Error ? error.message : t("auth.errors.unknownError");
      toast.error(errorMessage);
    } finally {
      setIsRedeeming(false);
    }
  };

  const formatNumber = (num: number | string): string => {
    if (typeof num === "string") {
      num = parseFloat(num);
    }
    if (isNaN(num) || !isFinite(num)) {
      return "0";
    }
    const formatted = new Intl.NumberFormat("en-US", {
      minimumFractionDigits: 0,
      maximumFractionDigits: 7,
    }).format(num);
    
    return formatted.replace(/(\.\d*?[1-9])0+$/g, '$1').replace(/\.$/, '');
  };

  const formatDateTime = (date: Date) => {
    return date.toLocaleString("en-US", {
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hour12: false,
    });
  };

  const formatDateTimeRows = (dateStr: string) => {
    // 添加空值检查和无效日期处理
    if (!dateStr) {
      return { date: "--", time: "--" };
    }

    const date = new Date(dateStr);

    // 检查是否是有效日期
    if (isNaN(date.getTime()) || date.toString() === "Invalid Date") {
      return { date: "--", time: "--" };
    }

    // 根据语言版本选择时区
    const isChinese = i18n.language === "zh";
    
    let datePart: string;
    let timePart: string;

    if (isChinese) {
      // 中文版本：显示新加坡时区 (UTC+8)
      const sgTime = new Date(date.toLocaleString("en-US", { timeZone: "Asia/Singapore" }));
      const sgDateStr = sgTime.toLocaleDateString("en-CA"); // YYYY-MM-DD
      const sgTimeStr = sgTime.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", hour12: false });
      datePart = sgDateStr;
      timePart = sgTimeStr;
    } else {
      // 英文版本：显示 UTC 时区
      const isoString = date.toISOString(); // "2024-12-01T00:00:00.000Z"
      datePart = isoString.substring(0, 10);
      timePart = isoString.substring(11, 16); // "00:00"
    }

    return { date: datePart, time: timePart };
  };

  const getInterestCalculation = (): InterestCalculationResult => {
    if (!selectedProduct || !amount) {
      return {
        principal: 0,
        days: 0,
        interest: 0,
        totalAmount: 0,
        dailyRate: 0,
        rate: 0
      };
    }
    
    const principal = parseFloat(amount);
    if (isNaN(principal) || principal <= 0) {
      return {
        principal: 0,
        days: 0,
        interest: 0,
        totalAmount: 0,
        dailyRate: 0,
        rate: 0
      };
    }
    
    // Use enhanced interest calculation with 'act_365' method for financial accuracy
    const result = calculateInterest(
      principal, 
      selectedProduct.apy / 100, 
      selectedProduct.duration, 
      'act_365'
    );
    
    return result;
  };

  const STATUS_MAP: Record<number, { label: string; variant: "default" | "secondary" | "outline" | "destructive" }> = {
    0: { label: "pending_interest", variant: "secondary" },
    1: { label: "accruing_interest", variant: "default" },
    2: { label: "renewed", variant: "outline" },
    3: { label: "settled", variant: "outline" },
    4: { label: "redeemed", variant: "destructive" },
  };

  const validateAmountInput = (): { valid: boolean; error?: string } => {
    if (!selectedProduct || !amount) {
      return { valid: true };
    }

    const amountNum = parseFloat(amount);
    const availableBalance = parseFloat(assets.find(a => a.currency === selectedProduct.currency)?.available || '0');

    // Use enhanced validation utility with translation support
    return validateAmount(amountNum, selectedProduct.minAmount, selectedProduct.maxAmount, availableBalance, t);
  };

  const getStatusInfo = (status: number) => {
    const statusConfig = STATUS_MAP[status];
    if (!statusConfig) {
      return { label: t("dashboard.fixedDepositHistory.statusUnknown"), variant: "outline" as const };
    }
    const labelKey = `dashboard.fixedDepositHistory.status_${statusConfig.label}`;
    return {
      label: t(labelKey),
      variant: statusConfig.variant
    };
  };

  const getDaysRemaining = (endDate: string) => {
    // 添加空值检查
    if (!endDate) return 0;

    const end = new Date(endDate);

    // 检查是否是有效日期
    if (isNaN(end.getTime())) return 0;

    const now = new Date();
    const diff = end.getTime() - now.getTime();
    const days = Math.ceil(diff / (1000 * 60 * 60 * 24));
    return days > 0 ? days : 0;
  };

  return (
    <div className="space-y-8 animate-fade-in">
      <div className="flex flex-col gap-4">
        <div className="flex items-center justify-between gap-4">
          <Tabs value={activeTab} onValueChange={setActiveTab} className="w-auto">
            <TabsList className="grid w-full grid-cols-2 h-12">
              <TabsTrigger value="products" className="text-base gap-2 px-6">
                <TrendingUp size={18} />
                {t("dashboard.fixedDeposit.products")}
              </TabsTrigger>
              <TabsTrigger value="history" className="text-base gap-2 px-6">
                <History size={18} />
                {t("dashboard.fixedDeposit.history")}
              </TabsTrigger>
            </TabsList>
          </Tabs>
          
          <div>
            <h1 className="text-3xl font-bold tracking-tight">{t("dashboard.fixedDeposit.pageTitle")}</h1>
            <p className="text-muted-foreground">{t("dashboard.fixedDeposit.pageDesc")}</p>
          </div>
        </div>
        
        <div className="w-full">
          {isLoadingAssets ? (
            <div className="flex items-center gap-2">
              <RefreshCw className="animate-spin h-4 w-4 text-muted-foreground" />
              <span className="text-sm text-muted-foreground">{t("dashboard.fixedDeposit.loading")}</span>
            </div>
          ) : (
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-sm text-muted-foreground">{t("dashboard.fixedDeposit.filterByCurrency")}:</span>
              {displayAssets.length === 0 ? (
                <span className="text-sm text-muted-foreground">{t("dashboard.fixedDeposit.noAssets")}</span>
              ) : (
                displayAssets.map((asset) => (
                  <Button
                    key={asset.currency}
                    variant={currencyFilter === asset.currency ? "default" : "outline"}
                    size="sm"
                    onClick={() => setCurrencyFilter(asset.currency)}
                    className="gap-1"
                    disabled={loadingStates.assets}
                  >
                    <Wallet size={14} />
                    {asset.currency}
                    {loadingStates.assets && <RefreshCw className="animate-spin h-3 w-3 ml-1" />}
                  </Button>
                ))
              )}
            </div>
          )}
        </div>
      </div>

      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsContent value="products" className="mt-0 space-y-6">
          <div className="grid gap-4">
            <Card className="bg-card/50 border-border/50">
              <CardHeader>
                <div className="flex items-center justify-between gap-4">
                  <div className="flex items-center gap-2">
                    <TrendingUp size={20} className="text-primary" />
                    <CardTitle className="text-lg">{t("dashboard.fixedDeposit.availableProducts")}</CardTitle>
                  </div>
                  <div className="flex items-center gap-3">
                    {currencyFilter && (
                      <span className="text-sm text-muted-foreground font-mono">
                        {formatNumber(assets.find(a => a.currency === currencyFilter)?.available || 0)} {currencyFilter}
                      </span>
                    )}
                    <Dialog open={open} onOpenChange={setOpen}>
                      <DialogTrigger asChild>
                        <Button 
                          size="lg" 
                          className="gap-2 shadow-lg shadow-primary/20" 
                          disabled={!selectedProduct || isSubscribing}
                        >
                          <PiggyBank size={18} />
                          {isSubscribing ? t("dashboard.fixedDeposit.subscribing") : t("dashboard.fixedDeposit.subscribe")}
                        </Button>
                      </DialogTrigger>
                      <DialogContent className="sm:max-w-[500px] bg-card border-border">
                        <DialogHeader>
                          <DialogTitle>{selectedProduct?.name || t("dashboard.fixedDeposit.confirmSubscribe")}</DialogTitle>
                          <DialogDescription>{t("dashboard.fixedDeposit.subscribeDesc")}</DialogDescription>
                        </DialogHeader>
                        <form onSubmit={(e) => {
                          e.preventDefault();
                          setShowConfirmDialog(true);
                        }} className="space-y-6 py-4">
                          <div className="p-4 rounded-xl bg-secondary/50 border border-border space-y-3">
                            <div className="flex justify-between text-sm">
                              <span className="text-muted-foreground">{t("dashboard.fixedDeposit.productName")}</span>
                              <span className="font-medium">{selectedProduct?.name}</span>
                            </div>
                            <div className="flex justify-between text-sm">
                              <span className="text-muted-foreground">{t("dashboard.fixedDeposit.apy")}</span>
                              <span className="text-primary font-bold">{selectedProduct?.apy}%</span>
                            </div>
                            <div className="flex justify-between text-sm">
                              <span className="text-muted-foreground">{t("dashboard.fixedDeposit.duration")}</span>
                              <span className="font-medium">{selectedProduct?.duration} {t("dashboard.fixedDeposit.days")}</span>
                            </div>
                          </div>

                          <div className="space-y-2">
                            <Label>{t("dashboard.fixedDeposit.amount")} ({selectedProduct?.currency})</Label>
                            {selectedProduct && (
                              <div className="flex items-center justify-between text-sm p-3 rounded-lg bg-primary/5 border border-primary/20">
                                <span className="text-muted-foreground flex items-center gap-1.5">
                                  <Wallet size={16} className="text-primary" />
                                  {t("dashboard.fixedDeposit.available")}
                                </span>
                                <span className="font-mono font-bold text-primary">
                                  {formatNumber(assets.find(a => a.currency === selectedProduct.currency)?.available || 0)} {selectedProduct.currency}
                                </span>
                              </div>
                            )}
                            <Input
                              type="number"
                              placeholder={`${t("dashboard.fixedDeposit.minAmount")}: ${selectedProduct?.minAmount || 0} ${selectedProduct?.currency || 'USDT'}`}
                              value={amount}
                              onChange={(e) => setAmount(e.target.value)}
                              required
                             />
                              {(() => {
                                const validation = validateAmountInput();
                                return validation.valid ? null : (
                                  <p className="text-xs text-red-500">
                                    {validation.error}
                                  </p>
                                );
                              })()}
                          </div>

                          <div className="p-4 rounded-xl bg-primary/5 border border-primary/20 space-y-2">
                            <div className="flex justify-between text-sm">
                              <span className="text-muted-foreground">{t("dashboard.fixedDeposit.interest")}</span>
                              <span className="font-bold text-primary">
                                {formatNumber(getInterestCalculation().interest)} {selectedProduct?.currency}
                              </span>
                            </div>
                            <div className="flex justify-between text-xs text-muted-foreground">
                              <span>{t("dashboard.fixedDeposit.maturityDate")}</span>
                              <span>
                                {selectedProduct
                                  ? (() => {
                                      const dateCalc = calculateDepositDates(selectedProduct.duration, 'tomorrow');
                                      return formatDateTime(dateCalc.endDate);
                                    })()
                                  : "-"}
                              </span>
                            </div>
                          </div>

                          {selectedProduct?.autoRenew && (
                            <div className="flex items-center justify-between p-4 rounded-lg bg-secondary/50 border border-border">
                              <div className="flex items-center gap-3">
                                <RefreshCw size={18} className="text-primary" />
                                <div className="flex flex-col">
                                  <span className="text-sm font-medium">{t("dashboard.fixedDeposit.autoRenewTitle")}</span>
                                  <span className="text-xs text-muted-foreground">{t("dashboard.fixedDeposit.autoRenewDesc")}</span>
                                </div>
                              </div>
                              <Switch
                                checked={autoRenew}
                                onCheckedChange={setAutoRenew}
                              />
                            </div>
                          )}

                          <div className="flex gap-2 p-3 rounded-lg bg-blue-500/5 border border-blue-500/20">
                            <ShieldCheck size={16} className="text-blue-500 shrink-0" />
                            <p className="text-xs text-muted-foreground">
                              {t("dashboard.fixedDeposit.securityNotice")}
                            </p>
                          </div>

                           <DialogFooter>
                             <Button type="button" variant="outline" onClick={() => setOpen(false)}>{t("common.cancel")}</Button>
                              <Button 
                                type="submit" 
                                className="w-full" 
                                disabled={
                                  !amount || 
                                  !validateAmountInput().valid ||
                                  isSubscribing
                                }
                              >
                                {t("dashboard.fixedDeposit.confirmSubscribe")}
                              </Button>
                          </DialogFooter>
                        </form>
                      </DialogContent>
                    </Dialog>

                    {/* 二次确认对话框 */}
                    <Dialog open={showConfirmDialog} onOpenChange={setShowConfirmDialog}>
                      <DialogContent className="sm:max-w-[450px] bg-card border-border">
                        <DialogHeader>
                          <DialogTitle>{t("dashboard.fixedDeposit.confirmSubscribe")}</DialogTitle>
                          <DialogDescription>{t("dashboard.fixedDeposit.confirmSubscribeDesc")}</DialogDescription>
                        </DialogHeader>
                        <div className="space-y-4 py-4">
                          <div className="p-4 rounded-xl bg-red-500/5 border border-red-500/20">
                            <div className="flex items-center gap-2 mb-3">
                              <AlertTriangle size={20} className="text-red-500" />
                              <span className="text-sm font-medium text-red-500">{t("dashboard.fixedDeposit.confirmWarning")}</span>
                            </div>
                            <div className="space-y-2 text-sm">
                              <div className="flex justify-between">
                                <span className="text-muted-foreground">{t("dashboard.fixedDeposit.productName")}:</span>
                                <span className="font-medium">{selectedProduct?.name}</span>
                              </div>
                              <div className="flex justify-between">
                                <span className="text-muted-foreground">{t("dashboard.fixedDeposit.amount")}:</span>
                                <span className="font-mono font-bold">{formatNumber(amount)} {selectedProduct?.currency}</span>
                              </div>
                              <div className="flex justify-between">
                                <span className="text-muted-foreground">{t("dashboard.fixedDeposit.apy")}:</span>
                                <span className="font-bold text-primary">{selectedProduct?.apy}%</span>
                              </div>
                              <div className="flex justify-between">
                                <span className="text-muted-foreground">{t("dashboard.fixedDeposit.interest")}:</span>
                                <span className="font-bold text-green-400">{formatNumber(getInterestCalculation().interest)} {selectedProduct?.currency}</span>
                              </div>
                              <div className="flex justify-between">
                                <span className="text-muted-foreground">{t("dashboard.fixedDeposit.duration")}:</span>
                                <span>{selectedProduct?.duration} {t("dashboard.fixedDeposit.days")}</span>
                              </div>
                              {autoRenew && (
                                <div className="flex justify-between">
                                  <span className="text-muted-foreground">{t("dashboard.fixedDeposit.autoRenew")}:</span>
                                  <span className="text-primary">{t("dashboard.fixedDeposit.yes")}</span>
                                </div>
                              )}
                            </div>
                          </div>
                          <div className="text-xs text-muted-foreground">
                            {t("dashboard.fixedDeposit.confirmNotice")}
                          </div>
                        </div>
                        <DialogFooter className="flex gap-2">
                          <Button 
                            variant="outline" 
                            onClick={() => setShowConfirmDialog(false)}
                            disabled={isSubscribing}
                          >
                            {t("common.cancel")}
                          </Button>
                          <Button 
                            onClick={handleSubscribe}
                            disabled={isSubscribing}
                            className="bg-red-500 hover:bg-red-600 text-white"
                          >
                            {isSubscribing ? (
                              <>
                                <RefreshCw size={16} className="animate-spin mr-2" />
                                {t("dashboard.fixedDeposit.subscribing")}
                              </>
                            ) : (
                              t("dashboard.fixedDeposit.confirmAndSubscribe")
                            )}
                          </Button>
                        </DialogFooter>
                      </DialogContent>
                    </Dialog>
                  </div>
                </div>
                <CardDescription>{t("dashboard.fixedDeposit.productDesc")}</CardDescription>
              </CardHeader>
              <CardContent>
                 {filteredProducts.length === 0 ? (
                   <div className="h-48 flex flex-col items-center justify-center text-muted-foreground gap-3">
                     <Info size={32} className="text-muted-foreground/50" />
                     <span className="text-lg">{t("dashboard.fixedDeposit.noProducts")}</span>
                      {currencyFilter && (
                        <span className="text-sm">
                          {t("dashboard.fixedDeposit.tryOtherCurrency")}
                        </span>
                      )}
                   </div>
                 ) : (
                   <>
                     <Table>
                       <TableHeader>
                         <TableRow className="hover:bg-transparent border-border/50">
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDeposit.productName")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDeposit.currency")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDeposit.apy")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDeposit.duration")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDeposit.minAmount")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDeposit.maxAmount")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDeposit.autoRenew")}</TableHead>
                           <TableHead className="text-muted-foreground text-right">{t("dashboard.fixedDeposit.action")}</TableHead>
                         </TableRow>
                       </TableHeader>
                       <TableBody>
                         {filteredProducts.map((product) => (
                           <TableRow 
                             key={product.id} 
                             className="border-border/50 hover:bg-secondary/30 transition-colors group cursor-pointer"
                             onClick={() => setSelectedProduct(product)}
                           >
                             <TableCell>
                               <div className="flex items-center gap-2">
                                 <PiggyBank size={16} className="text-primary" />
                                 <span className="font-medium">{product.name}</span>
                               </div>
                             </TableCell>
                             <TableCell>
                               <Badge variant="secondary" className="font-mono">{product.currency}</Badge>
                             </TableCell>
                             <TableCell>
                               <Badge variant="outline" className="bg-primary/10 text-primary border-primary/20 font-bold">
                                 {product.apy}%
                               </Badge>
                             </TableCell>
                             <TableCell>
                               <div className="flex items-center gap-1 text-sm">
                                 <Clock size={14} className="text-muted-foreground" />
                                 <span>{product.duration} {t("dashboard.fixedDeposit.days")}</span>
                               </div>
                             </TableCell>
                             <TableCell className="font-mono text-sm">{formatNumber(product.minAmount)} {product.currency}</TableCell>
                             <TableCell className="font-mono text-sm">{formatNumber(product.maxAmount)} {product.currency}</TableCell>
                             <TableCell>
                               {product.autoRenew ? (
                                 <Badge variant="default" className="gap-1">
                                   <RefreshCw size={12} />
                                   {t("dashboard.fixedDeposit.support")}
                                 </Badge>
                               ) : (
                                 <Badge variant="outline" className="text-muted-foreground">
                                   {t("dashboard.fixedDeposit.notSupport")}
                                 </Badge>
                               )}
                              </TableCell>
                              <TableCell className="text-right">
                                <Button 
                                  size="sm" 
                                  variant={selectedProduct?.id === product.id ? "default" : "ghost"}
                                  className="gap-1"
                                >
                                  {selectedProduct?.id === product.id ? t("dashboard.fixedDeposit.selected") : t("dashboard.fixedDeposit.select")}
                                  <ArrowRight size={14} />
                                </Button>
                              </TableCell>
                           </TableRow>
                         ))}
                       </TableBody>
                     </Table>
                     {filteredProducts.length > 0 && (
                       <Pagination 
                         currentPage={productsPage} 
                         totalPages={Math.max(1, Math.ceil(productsTotal / 10))} 
                         onPageChange={setProductsPage}
                       />
                     )}
                   </>
                  )}
               </CardContent>
            </Card>

            <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
              <Card className="bg-gradient-to-br from-primary/10 to-transparent border-primary/20">
                <CardContent className="pt-6">
                  <div className="flex items-center gap-2 mb-2">
                    <ShieldCheck className="text-primary" size={20} />
                    <span className="font-medium">{t("dashboard.fixedDeposit.security")}</span>
                  </div>
                  <p className="text-sm text-muted-foreground">{t("dashboard.fixedDeposit.securityDesc")}</p>
                </CardContent>
              </Card>
              
              <Card className="bg-gradient-to-br from-green-500/10 to-transparent border-green-500/20">
                <CardContent className="pt-6">
                  <div className="flex items-center gap-2 mb-2">
                    <Percent className="text-green-500" size={20} />
                    <span className="font-medium">{t("dashboard.fixedDeposit.stableReturns")}</span>
                  </div>
                  <p className="text-sm text-muted-foreground">{t("dashboard.fixedDeposit.stableReturnsDesc")}</p>
                </CardContent>
              </Card>
              
              <Card className="bg-gradient-to-br from-blue-500/10 to-transparent border-blue-500/20">
                <CardContent className="pt-6">
                  <div className="flex items-center gap-2 mb-2">
                    <Info className="text-blue-500" size={20} />
                    <span className="font-medium">{t("dashboard.fixedDeposit.flexibleTerms")}</span>
                  </div>
                  <p className="text-sm text-muted-foreground">{t("dashboard.fixedDeposit.flexibleTermsDesc")}</p>
                </CardContent>
              </Card>
            </div>
          </div>
        </TabsContent>

        <TabsContent value="history" className="mt-0 space-y-6">
          <div className="grid gap-6">
            <Card className="bg-card/50 border-border/50">
              <CardHeader>
                <CardTitle className="text-lg flex items-center gap-2">
                  <History size={20} className="text-primary" />
                  {t("dashboard.fixedDepositHistory.title")}
                </CardTitle>
                <CardDescription>{t("dashboard.fixedDepositHistory.description")}</CardDescription>
              </CardHeader>
              <CardContent>
                 {filteredOrders.length === 0 ? (
                   <div className="h-32 flex flex-col items-center justify-center text-muted-foreground gap-2 border-2 border-dashed border-border rounded-xl">
                     <Info size={24} />
                     <span>{t("dashboard.fixedDepositHistory.noOrders")}</span>
                   </div>
                 ) : (
                   <>
                     <Table>
                       <TableHeader>
                         <TableRow className="hover:bg-transparent border-border/50">
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDepositHistory.orderNo")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDeposit.productName")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDepositHistory.amount")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDepositHistory.expectedReturn")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDepositHistory.paidReturn")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDepositHistory.accruedReturn")}</TableHead>
                            <TableHead className="text-muted-foreground">{t("dashboard.fixedDepositHistory.duration")}</TableHead>
                            <TableHead className="text-muted-foreground">{t("dashboard.fixedDepositHistory.startDate")}</TableHead>
                            <TableHead className="text-muted-foreground">{t("dashboard.fixedDepositHistory.maturity")}</TableHead>
                           <TableHead className="text-muted-foreground">{t("dashboard.fixedDepositHistory.status")}</TableHead>
                           <TableHead className="text-muted-foreground text-right">{t("dashboard.fixedDepositHistory.action")}</TableHead>
                         </TableRow>
                       </TableHeader>
                       <TableBody>
                         {filteredOrders.map((order) => {
                           const statusInfo = getStatusInfo(order.status);
                           const daysRemaining = order.status === 1 ? getDaysRemaining(order.endDate) : null;
                           return (
                             <TableRow key={order.id} className="border-border/50 hover:bg-secondary/30 transition-colors group">
                               <TableCell className="font-mono text-xs text-muted-foreground">
                                 #{order.id.toString().padStart(6, '0')}
                               </TableCell>
                               <TableCell>
                                 <div className="flex items-center gap-2">
                                   <PiggyBank size={14} className="text-primary" />
                                   <span className="font-medium">{order.productTitle}</span>
                                 </div>
                               </TableCell>
                               <TableCell className="font-mono">{formatNumber(order.amount)} {order.currency}</TableCell>
                               <TableCell className="font-mono text-primary">{formatNumber(order.interestExpected)} {order.currency}</TableCell>
                               <TableCell className="font-mono text-green-400">{formatNumber(order.interestPaid)} {order.currency}</TableCell>
                               <TableCell className="font-mono font-bold text-green-400">+{formatNumber(order.interestAccrued)} {order.currency}</TableCell>
                                 <TableCell>
                                   <div className="flex items-center gap-1 text-sm">
                                     <Clock size={14} className="text-muted-foreground" />
                                     <span>{order.duration ? `${order.duration} ${t("dashboard.fixedDeposit.days")}` : "--"}</span>
                                   </div>
                                 </TableCell>
                                <TableCell>
                                  <div className="flex flex-col text-sm">
                                    {(() => {
                                      const start = formatDateTimeRows(order.startDate);
                                      return (
                                        <>
                                          <span>{start.date}</span>
                                          <span className="text-xs text-muted-foreground">{start.time}</span>
                                        </>
                                      );
                                    })()}
                                  </div>
                                </TableCell>
                               <TableCell>
                                 <div className="flex flex-col">
                                   {(() => {
                                     const end = formatDateTimeRows(order.endDate);
                                     return (
                                       <>
                                         <span className="text-sm">{end.date}</span>
                                         <span className="text-xs text-muted-foreground">{end.time}</span>
                                         {daysRemaining > 0 && (
                                           <span className="text-xs text-muted-foreground">{t("dashboard.fixedDepositHistory.remainingDays")} {daysRemaining} {t("dashboard.fixedDepositHistory.days")}</span>
                                         )}
                                       </>
                                     );
                                   })()}
                                 </div>
                               </TableCell>
                               <TableCell>
                                 <Badge variant={statusInfo.variant} className="w-full justify-center">{statusInfo.label}</Badge>
                               </TableCell>
                               <TableCell className="text-right">
                                 {order.status === 1 && (
                                   <Button size="sm" variant="destructive" className="gap-1" onClick={() => { setSelectedOrder(order); setRedeemAmount(order.amount.toString()); setRedeemDialogOpen(true); }}>
                                     <RefreshCw size={12} />{t("dashboard.fixedDepositHistory.redeem")}
                                   </Button>
                                 )}
                                 {order.status === 2 && (
                                   <Button size="sm" variant="default" className="gap-1" onClick={() => { setSelectedOrder(order); setRedeemAmount(order.amount.toString()); setRedeemDialogOpen(true); }}>
                                     <ArrowRight size={12} />{t("dashboard.fixedDepositHistory.claim")}
                                   </Button>
                                 )}
                               </TableCell>
                             </TableRow>
                           );
                         })}
                       </TableBody>
                     </Table>
                     {filteredOrders.length > 0 && (
                       <Pagination 
                         currentPage={ordersPage} 
                         totalPages={Math.max(1, Math.ceil(ordersTotal / 20))} 
                         onPageChange={setOrdersPage}
                       />
                     )}
                   </>
                   )}
                 </CardContent>
            </Card>

            {currencyFilter ? (
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Card className="bg-gradient-to-br from-green-500/10 to-transparent border-green-500/20">
                  <CardContent className="pt-6">
                    <div className="flex items-center gap-2 mb-2">
                      <TrendingUp className="text-green-500" size={20} />
                      <span className="font-medium">{t("dashboard.fixedDepositHistory.totalPaidReturn")} ({currencyFilter})</span>
                    </div>
                    <p className="text-2xl font-bold text-green-400">+{formatNumber(filteredOrders.reduce((sum, o) => sum + parseFloat(o.interestPaid.toString()), 0))} {currencyFilter}</p>
                    <p className="text-sm text-muted-foreground">{t("dashboard.fixedDepositHistory.totalPaidReturnDesc")}</p>
                  </CardContent>
                </Card>

                <Card className="bg-gradient-to-br from-primary/10 to-transparent border-primary/20">
                  <CardContent className="pt-6">
                    <div className="flex items-center gap-2 mb-2">
                      <PiggyBank className="text-primary" size={20} />
                      <span className="font-medium">{t("dashboard.fixedDepositHistory.principal")} ({currencyFilter})</span>
                    </div>
                    <p className="text-2xl font-bold">{formatNumber(filteredOrders.filter(o => o.status === 1).reduce((sum, o) => sum + parseFloat(o.amount.toString()), 0))} {currencyFilter}</p>
                    <p className="text-sm text-muted-foreground">{t("dashboard.fixedDepositHistory.principalDesc")}</p>
                  </CardContent>
                </Card>

                <Card className="bg-gradient-to-br from-blue-500/10 to-transparent border-blue-500/20">
                  <CardContent className="pt-6">
                    <div className="flex items-center gap-2 mb-2">
                      <History className="text-blue-500" size={20} />
                      <span className="font-medium">{t("dashboard.fixedDepositHistory.completedOrders")} ({currencyFilter})</span>
                    </div>
                    <p className="text-2xl font-bold">{filteredOrders.filter(o => o.status === 3).length} {t("dashboard.fixedDepositHistory.orders")}</p>
                    <p className="text-sm text-muted-foreground">{t("dashboard.fixedDepositHistory.completedOrdersDesc")}</p>
                  </CardContent>
                </Card>
              </div>
            ) : (
              <Card className="bg-card/50 border-border/50">
                <CardContent className="pt-6">
                  <div className="flex flex-col items-center justify-center gap-2 text-muted-foreground">
                    <Info size={24} />
                    <span>{t("dashboard.fixedDepositHistory.selectCurrencyHint")}</span>
                  </div>
                </CardContent>
              </Card>
            )}
          </div>
        </TabsContent>
      </Tabs>

      <Dialog open={redeemDialogOpen} onOpenChange={setRedeemDialogOpen}>
        <DialogContent className="sm:max-w-[500px] bg-card border-border">
          <DialogHeader>
            <DialogTitle>{selectedOrder?.status === 2 ? t("dashboard.fixedDepositHistory.claim") : t("dashboard.fixedDepositHistory.redeem")}</DialogTitle>
            <DialogDescription>{selectedOrder?.productTitle} - {formatNumber(selectedOrder?.amount || 0)} {selectedOrder?.currency}</DialogDescription>
          </DialogHeader>
          <form onSubmit={handleRedeem} className="space-y-6 py-4">
            {selectedOrder?.status === 1 && (
              <div className="flex gap-2 p-3 rounded-lg bg-red-500/10 border border-red-500/30">
                <AlertTriangle size={16} className="text-red-500 shrink-0" />
                <p className="text-xs text-red-400">{t("dashboard.fixedDepositHistory.earlyRedeemWarning")}</p>
              </div>
            )}

            <div className="p-4 rounded-xl bg-secondary/50 border border-border space-y-3">
              <div className="flex justify-between text-sm">
                <span className="text-muted-foreground">{t("dashboard.fixedDepositHistory.amount")}</span>
                <span className="font-medium">{formatNumber(selectedOrder?.amount || 0)} {selectedOrder?.currency}</span>
              </div>
              {selectedOrder?.status === 2 ? (
                <>
                  <div className="flex justify-between text-sm">
                    <span className="text-muted-foreground">{t("dashboard.fixedDepositHistory.accruedReturn")}</span>
                    <span className="font-medium text-green-400">+{formatNumber(selectedOrder?.interestAccrued || 0)} {selectedOrder?.currency}</span>
                  </div>
                  <div className="flex justify-between text-sm">
                    <span className="text-muted-foreground">{t("dashboard.fixedDepositHistory.paidReturn")}</span>
                    <span className="font-medium text-green-400">+{formatNumber(selectedOrder?.interestPaid || 0)} {selectedOrder?.currency}</span>
                  </div>
                </>
              ) : (
                <div className="flex justify-between text-sm">
                  <span className="text-muted-foreground">{t("dashboard.fixedDepositHistory.expectedReturn")}</span>
                  <span className="text-muted-foreground">{t("dashboard.fixedDepositHistory.noReturn")}</span>
                </div>
              )}
              <div className="border-t border-border pt-2 mt-2">
                <div className="flex justify-between text-sm">
                  <span className="text-muted-foreground font-medium">{selectedOrder?.status === 2 ? t("dashboard.fixedDepositHistory.estimatedAmount") : t("dashboard.fixedDepositHistory.redeemAmount")}</span>
                  <span className={`font-bold ${selectedOrder?.status === 2 ? "text-primary" : "text-foreground"}`}>
                    {selectedOrder?.status === 2 ? (
                      <>{formatNumber(Number(selectedOrder?.amount || 0) + Number(selectedOrder?.interestAccrued || 0) + Number(selectedOrder?.interestPaid || 0))} {selectedOrder?.currency}</>
                    ) : (
                      <><span className="text-muted-foreground">{formatNumber(selectedOrder?.amount || 0)} {selectedOrder?.currency} ({t("dashboard.fixedDepositHistory.principalOnly")})</span></>
                    )}
                  </span>
                </div>
              </div>
            </div>

            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setRedeemDialogOpen(false)}>{t("common.cancel")}</Button>
              <Button type="submit" className="w-full" disabled={isRedeeming}>
                {isRedeeming ? t("dashboard.fixedDepositHistory.redeeming") : (selectedOrder?.status === 2 ? t("dashboard.fixedDepositHistory.confirmClaim") : t("dashboard.fixedDepositHistory.confirmRedeem"))}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
};

export default FixedDeposit;
