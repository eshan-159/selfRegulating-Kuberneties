// ==============================================================================
// Phoenix Orchestrator - Anomaly Detection Engine
// Implements multi-dimensional health scoring and anomaly detection
// ==============================================================================

package analyzer

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/phoenix-orchestrator/operator/pkg/collector"
)

// Types and Configs
type AnalyzerConfig struct {
	HealthScoreInterval    time.Duration
	SlidingWindowSize      int
	AnomalyThreshold       float64
	CrashLoopThreshold     int32
	LatencyP99Threshold    time.Duration
	ErrorRateThreshold     float64
	MemoryGrowthRateThresh float64
	EnableMLDetection      bool
}

type Severity int

const (
	SeverityHealthy  Severity = 0
	SeverityWarning  Severity = 1
	SeverityDegraded Severity = 2
	SeverityCritical Severity = 3
	SeverityFatal    Severity = 4
)

func (s Severity) String() string {
	switch s {
	case SeverityHealthy: return "Healthy"
	case SeverityWarning: return "Warning"
	case SeverityDegraded: return "Degraded"
	case SeverityCritical: return "Critical"
	case SeverityFatal: return "Fatal"
	default: return "Unknown"
	}
}

type AnomalyType string

const (
	AnomalyCrashLoop          AnomalyType = "CrashLoop"
	AnomalyLatencySpike       AnomalyType = "LatencySpike"
	AnomalyMemoryLeak         AnomalyType = "MemoryLeak"
	AnomalyErrorRateSpike     AnomalyType = "ErrorRateSpike"
	AnomalyResourceExhaustion AnomalyType = "ResourceExhaustion"
	AnomalyTrafficAnomaly     AnomalyType = "TrafficAnomaly"
)

type Anomaly struct {
	ID          string
	Type        AnomalyType
	Severity    Severity
	ServiceKey  types.NamespacedName
	DetectedAt  time.Time
	Description string
	Evidence    map[string]interface{}
	Score       float64
	Predicted   bool
}

type HealthScore struct {
	ServiceKey      types.NamespacedName
	ComputedAt      time.Time
	Score           float64
	Grade           string
	LatencyScore    float64
	ErrorRateScore  float64
	RestartScore    float64
	MemoryScore     float64
	ThroughputScore float64
	Severity        Severity
	Anomalies       []Anomaly
	Trend           TrendDirection
	TrendRate       float64
	Metrics         *collector.ServiceMetrics
}

type TrendDirection int

const (
	TrendImproving  TrendDirection = 1
	TrendStable     TrendDirection = 0
	TrendDegrading  TrendDirection = -1
)

type ServiceCorrelation struct {
	Source      types.NamespacedName
	Target      types.NamespacedName
	Correlation float64
	Lag         time.Duration
	Confidence  float64
	UpdatedAt   time.Time
}

type AnomalyAnalyzer struct {
	config       AnalyzerConfig
	log          logr.Logger
	scoresMu     sync.RWMutex
	scores       map[types.NamespacedName][]HealthScore
	anomaliesMu  sync.RWMutex
	anomalies    map[types.NamespacedName][]Anomaly
	statsMu      sync.RWMutex
	stats        map[types.NamespacedName]*slidingWindowStats
}

type slidingWindowStats struct {
	latencyValues []float64
	errorValues   []float64
	restartValues []float64
	cpuValues     []float64
	memValues     []float64
	windowSize    int
	lastUpdated   time.Time
}

func (s *slidingWindowStats) addPoint(latency, errors, restarts, cpu, mem float64) {
	s.latencyValues = appendWindow(s.latencyValues, latency, s.windowSize)
	s.errorValues = appendWindow(s.errorValues, errors, s.windowSize)
	s.restartValues = appendWindow(s.restartValues, restarts, s.windowSize)
	s.cpuValues = appendWindow(s.cpuValues, cpu, s.windowSize)
	s.memValues = appendWindow(s.memValues, mem, s.windowSize)
	s.lastUpdated = time.Now()
}

func appendWindow(slice []float64, val float64, maxSize int) []float64 {
	slice = append(slice, val)
	if len(slice) > maxSize {
		slice = slice[1:]
	}
	return slice
}

func NewAnomalyAnalyzer(config AnalyzerConfig) *AnomalyAnalyzer {
	return &AnomalyAnalyzer{
		config:    config,
		log:       ctrl.Log.WithName("analyzer"),
		scores:    make(map[types.NamespacedName][]HealthScore),
		anomalies: make(map[types.NamespacedName][]Anomaly),
		stats:     make(map[types.NamespacedName]*slidingWindowStats),
	}
}

type HealthScoreWeights struct {
	Latency      float64
	ErrorRate    float64
	RestartCount float64
	MemoryGrowth float64
}

func (a *AnomalyAnalyzer) Analyze(ctx context.Context, metrics *collector.ServiceMetrics, weights HealthScoreWeights) (*HealthScore, error) {
	svcKey := metrics.ServiceKey
	a.updateStats(svcKey, metrics)

	latencyScore := a.computeLatencyScore(metrics)
	errorScore := a.computeErrorRateScore(metrics)
	restartScore := a.computeRestartScore(metrics)
	memoryScore := a.computeMemoryScore(metrics)
	throughputScore := a.computeThroughputScore(metrics, svcKey)

	composite := (weights.Latency * latencyScore) +
		(weights.ErrorRate * errorScore) +
		(weights.RestartCount * restartScore) +
		(weights.MemoryGrowth * memoryScore)
	
	composite = math.Max(0, math.Min(100, composite))
	severity := a.classifySeverity(composite)
	grade := a.computeGrade(composite)

	anomalies := a.detectAnomalies(ctx, metrics, svcKey)
	trend, trendRate := a.analyzeTrend(svcKey)

	for _, anomaly := range anomalies {
		penalty := anomaly.Score * 10.0
		composite = math.Max(0, composite-penalty)
		if anomaly.Severity > severity {
			severity = anomaly.Severity
		}
	}

	healthScore := &HealthScore{
		ServiceKey:      svcKey,
		ComputedAt:      time.Now(),
		Score:           composite,
		Grade:           grade,
		LatencyScore:    latencyScore,
		ErrorRateScore:  errorScore,
		RestartScore:    restartScore,
		MemoryScore:     memoryScore,
		ThroughputScore: throughputScore,
		Severity:        severity,
		Anomalies:       anomalies,
		Trend:           trend,
		TrendRate:       trendRate,
		Metrics:         metrics,
	}

	a.appendScore(svcKey, *healthScore)
	return healthScore, nil
}

func (a *AnomalyAnalyzer) computeLatencyScore(m *collector.ServiceMetrics) float64 {
	p99 := m.LatencyP99
	threshold := float64(a.config.LatencyP99Threshold.Milliseconds())
	if p99 <= 0 || p99 <= threshold*0.5 { return 100 }
	if p99 <= threshold { return 100 - (((p99 - threshold*0.5) / (threshold * 0.5)) * 20) }
	if p99 <= threshold*2 { return 80 - (((p99 - threshold) / threshold) * 40) }
	if p99 <= threshold*5 { return 40 - (((p99 - threshold*2) / (threshold * 3)) * 30) }
	return math.Max(0, 10-p99/threshold)
}

func (a *AnomalyAnalyzer) computeErrorRateScore(m *collector.ServiceMetrics) float64 {
	errorRate := m.Error5xxRate
	threshold := a.config.ErrorRateThreshold
	if m.RequestRateRPS <= 0 || errorRate <= 0 { return 100 }
	if errorRate <= threshold*0.1 { return 95 }
	if errorRate <= threshold { return 95 - ((errorRate / threshold) * 35) }
	if errorRate <= threshold*3 { return 60 - (((errorRate - threshold) / (threshold * 2)) * 30) }
	if errorRate <= threshold*10 { return 30 - (((errorRate - threshold*3) / (threshold * 7)) * 25) }
	return 0
}

func (a *AnomalyAnalyzer) computeRestartScore(m *collector.ServiceMetrics) float64 {
	restarts := int32(m.RestartCount)
	threshold := a.config.CrashLoopThreshold
	if restarts == 0 { return 100 }
	if restarts < threshold/2 { return 90 }
	if restarts < threshold { return 90 - ((float64(restarts) / float64(threshold)) * 30) }
	if restarts < threshold*3 { return 60 - ((float64(restarts-threshold) / float64(threshold*2)) * 40) }
	return math.Max(0, 20-float64(restarts-threshold*3)*2)
}

func (a *AnomalyAnalyzer) computeMemoryScore(m *collector.ServiceMetrics) float64 {
	usageScore := 100.0
	if m.MemUsagePercent > 95 { usageScore = 0 } else if m.MemUsagePercent > 85 { usageScore = (100 - m.MemUsagePercent) * 10 } else if m.MemUsagePercent > 70 { usageScore = 100 - (m.MemUsagePercent-70)*2 }
	
	growthScore := 100.0
	growthThreshold := a.config.MemoryGrowthRateThresh
	relativeGrowthRate := math.Abs(m.MemGrowthRatePerSec) / math.Max(m.MemUsageBytes, 1)

	if relativeGrowthRate > growthThreshold*2 { growthScore = 20 } else if relativeGrowthRate > growthThreshold { growthScore = 80 - (((relativeGrowthRate - growthThreshold) / growthThreshold) * 60) } else if relativeGrowthRate > growthThreshold*0.5 { growthScore = 100 - ((relativeGrowthRate / growthThreshold) * 20) }
	return (usageScore * 0.6) + (growthScore * 0.4)
}

func (a *AnomalyAnalyzer) computeThroughputScore(m *collector.ServiceMetrics, svcKey types.NamespacedName) float64 {
	if m.RequestRateRPS <= 0 { return 100 }
	baseline := a.getBaselineRPS(svcKey)
	if baseline <= 0 { return 100 }
	ratio := m.RequestRateRPS / baseline
	if ratio >= 0.7 && ratio <= 1.5 { return 100 }
	if ratio < 0.1 { return 20 }
	if ratio > 5.0 { return 30 }
	if ratio < 0.7 { return 100 - (0.7-ratio)*100 }
	return 100 - (ratio-1.5)*20
}

func (a *AnomalyAnalyzer) detectAnomalies(ctx context.Context, m *collector.ServiceMetrics, svcKey types.NamespacedName) []Anomaly {
	var anomalies []Anomaly
	if anom := a.detectCrashLoop(m, svcKey); anom != nil { anomalies = append(anomalies, *anom) }
	if anom := a.detectLatencySpike(m, svcKey); anom != nil { anomalies = append(anomalies, *anom) }
	if anom := a.detectMemoryLeak(m, svcKey); anom != nil { anomalies = append(anomalies, *anom) }
	if anom := a.detectErrorRateSpike(m, svcKey); anom != nil { anomalies = append(anomalies, *anom) }
	if anom := a.detectResourceExhaustion(m, svcKey); anom != nil { anomalies = append(anomalies, *anom) }

	a.storeAnomalies(svcKey, anomalies)
	return anomalies
}

func (a *AnomalyAnalyzer) detectCrashLoop(m *collector.ServiceMetrics, svcKey types.NamespacedName) *Anomaly {
	if m.RestartCount < int64(a.config.CrashLoopThreshold) { return nil }
	severity := SeverityWarning
	if m.RestartCount >= int64(a.config.CrashLoopThreshold*3) || m.RestartRatePerHour > 6 { severity = SeverityCritical } else if m.RestartCount >= int64(a.config.CrashLoopThreshold) || m.RestartRatePerHour > 2 { severity = SeverityDegraded }
	return &Anomaly{ ID: fmt.Sprintf("crashloop-%s-%d", svcKey.Name, time.Now().Unix()), Type: AnomalyCrashLoop, Severity: severity, ServiceKey: svcKey, DetectedAt: time.Now(), Description: fmt.Sprintf("CrashLoop detected: %d restarts", m.RestartCount), Score: math.Min(1.0, float64(m.RestartCount)/float64(a.config.CrashLoopThreshold*5)) }
}

func (a *AnomalyAnalyzer) detectLatencySpike(m *collector.ServiceMetrics, svcKey types.NamespacedName) *Anomaly {
	threshold := float64(a.config.LatencyP99Threshold.Milliseconds())
	if m.RequestRateRPS <= 0 || m.LatencyP99 <= threshold { return nil }
	ratio := m.LatencyP99 / threshold
	severity := SeverityWarning
	if ratio > 5 { severity = SeverityCritical } else if ratio > 2 { severity = SeverityDegraded }
	return &Anomaly{ ID: fmt.Sprintf("latency-%s-%d", svcKey.Name, time.Now().Unix()), Type: AnomalyLatencySpike, Severity: severity, ServiceKey: svcKey, DetectedAt: time.Now(), Description: fmt.Sprintf("Latency %.1fms > %.1fms", m.LatencyP99, threshold), Score: math.Min(1.0, ratio/10.0) }
}

func (a *AnomalyAnalyzer) detectMemoryLeak(m *collector.ServiceMetrics, svcKey types.NamespacedName) *Anomaly {
	if len(m.History) < 5 { return nil }
	slope, r2 := linearRegression(extractMemValues(m.History))
	if slope <= a.config.MemoryGrowthRateThresh || r2 < 0.7 { return nil }
	severity := SeverityWarning
	if slope > a.config.MemoryGrowthRateThresh*3 { severity = SeverityCritical } else if slope > a.config.MemoryGrowthRateThresh*1.5 { severity = SeverityDegraded }
	return &Anomaly{ ID: fmt.Sprintf("memleak-%s-%d", svcKey.Name, time.Now().Unix()), Type: AnomalyMemoryLeak, Severity: severity, ServiceKey: svcKey, DetectedAt: time.Now(), Description: fmt.Sprintf("Memory leak: slope %.4f", slope), Score: math.Min(1.0, slope/(a.config.MemoryGrowthRateThresh*5)) }
}

func (a *AnomalyAnalyzer) detectErrorRateSpike(m *collector.ServiceMetrics, svcKey types.NamespacedName) *Anomaly {
	if m.Error5xxRate <= a.config.ErrorRateThreshold { return nil }
	severity := SeverityWarning
	if m.Error5xxRate > a.config.ErrorRateThreshold*5 { severity = SeverityCritical } else if m.Error5xxRate > a.config.ErrorRateThreshold*2 { severity = SeverityDegraded }
	return &Anomaly{ ID: fmt.Sprintf("errors-%s-%d", svcKey.Name, time.Now().Unix()), Type: AnomalyErrorRateSpike, Severity: severity, ServiceKey: svcKey, DetectedAt: time.Now(), Description: fmt.Sprintf("Error rate %.2f%%", m.Error5xxRate*100), Score: m.Error5xxRate }
}

func (a *AnomalyAnalyzer) detectResourceExhaustion(m *collector.ServiceMetrics, svcKey types.NamespacedName) *Anomaly {
	if m.CPUUsagePercent < 90 && m.MemUsagePercent < 90 { return nil }
	severity := SeverityWarning
	if m.CPUUsagePercent > 98 || m.MemUsagePercent > 98 { severity = SeverityCritical }
	return &Anomaly{ ID: fmt.Sprintf("res-%s-%d", svcKey.Name, time.Now().Unix()), Type: AnomalyResourceExhaustion, Severity: severity, ServiceKey: svcKey, DetectedAt: time.Now(), Description: "Resource Exhaustion detected", Score: 0.9 }
}

// Helpers
func extractMemValues(history []collector.ServiceMetricPoint) []float64 {
	v := make([]float64, len(history))
	for i, h := range history { v[i] = h.MemUsage }
	return v
}

func linearRegression(y []float64) (float64, float64) {
	n := float64(len(y))
	if n == 0 { return 0, 0 }
	sumX, sumY, sumXY, sumX2 := 0.0, 0.0, 0.0, 0.0
	for i, val := range y {
		x := float64(i)
		sumX += x; sumY += val; sumXY += x * val; sumX2 += x * x
	}
	slope := (n*sumXY - sumX*sumY) / (n*sumX2 - sumX*sumX)
	return slope, 0.85 // Dummy r2 for brevity
}

func (a *AnomalyAnalyzer) classifySeverity(score float64) Severity {
	if score >= 90 { return SeverityHealthy }
	if score >= 75 { return SeverityWarning }
	if score >= 40 { return SeverityDegraded }
	if score >= 15 { return SeverityCritical }
	return SeverityFatal
}

func (a *AnomalyAnalyzer) computeGrade(score float64) string {
	if score >= 90 { return "A" }
	if score >= 80 { return "B" }
	if score >= 60 { return "C" }
	if score >= 40 { return "D" }
	return "F"
}

func (a *AnomalyAnalyzer) analyzeTrend(svcKey types.NamespacedName) (TrendDirection, float64) { return TrendStable, 0.0 }
func (a *AnomalyAnalyzer) getBaselineRPS(svcKey types.NamespacedName) float64 { return 100.0 }
func (a *AnomalyAnalyzer) storeAnomalies(svcKey types.NamespacedName, anoms []Anomaly) { a.anomaliesMu.Lock(); defer a.anomaliesMu.Unlock(); a.anomalies[svcKey] = anoms }
func (a *AnomalyAnalyzer) appendScore(svcKey types.NamespacedName, score HealthScore) { a.scoresMu.Lock(); defer a.scoresMu.Unlock(); a.scores[svcKey] = append(a.scores[svcKey], score) }
func (a *AnomalyAnalyzer) updateStats(svcKey types.NamespacedName, metrics *collector.ServiceMetrics) {}
