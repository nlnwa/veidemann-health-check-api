package healthcheck

import (
	"context"
	"fmt"
	"time"

	controllerApi "github.com/nlnwa/veidemann-api-go/controller/v1"
	"github.com/nlnwa/veidemann-health-check-api/pkg/client/controller"
	"github.com/nlnwa/veidemann-health-check-api/pkg/client/prometheus"
	"github.com/nlnwa/veidemann-health-check-api/pkg/client/web"
)

const (
	VeidemannDashboard     string = "veidemann:dashboard"
	VeidemannJobs          string = "veidemann:jobs"
	VeidemannCrawlerStatus string = "veidemann:crawlerStatus"
	VeidemannActivity      string = "veidemann:activity"
	VeidemannHarvest       string = "veidemann:harvest"
)

type Value interface {
}

type Status int

const (
	StatusUndefined = iota
	StatusPass
	StatusWarning
	StatusFail
)

type CheckResult struct {
	Name    string
	Results []*Result
}

type Result struct {
	Id          string
	Type        string
	Unit        string
	Endpoints   []string
	Links       []string
	Time        time.Time
	Status      Status
	Value       Value
	Err         error
	Description string
}

type checker func(context.Context) *Result

type component struct {
	id       string
	checkers []checker
}

type checkObserver func(*CheckResult)

type Options struct {
	WebOptions web.Options
	Controller controller.Options
	Prometheus prometheus.Options
}

type HealthChecker struct {
	httpClient       web.Query
	prometheusClient prometheus.Query
	controllerClient controller.Query
	components       []component
}

func NewHealthChecker(options *Options) *HealthChecker {
	hc := &HealthChecker{
		httpClient:       web.New(options.WebOptions),
		controllerClient: controller.New(options.Controller),
		prometheusClient: prometheus.New(options.Prometheus),
	}
	hc.components = hc.getChecks()
	return hc
}

func (hc *HealthChecker) RunChecks(observer checkObserver) {
	for _, component := range hc.components {
		var checkResults []*Result

		for _, checker := range component.checkers {
			checkResults = append(checkResults, hc.runCheck(checker))
		}
		result := &CheckResult{
			Name:    component.id,
			Results: checkResults,
		}
		observer(result)
	}
}

func (hc *HealthChecker) runCheck(checker checker) *Result {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
	defer cancel()
	return checker(ctx)
}

// getChecks returns a list of components to be checked
func (hc *HealthChecker) getChecks() []component {
	var veidemannRunStatus *controllerApi.RunStatus
	var veidemannIsActive bool
	var veidemannJobs []string

	return []component{
		{
			id: VeidemannDashboard,
			checkers: []checker{
				func(ctx context.Context) *Result {
					statusCode, status, err := hc.httpClient.CheckVeidemannDashboard(ctx)
					result := &Result{
						Description: "check veidemann dashboard is responding",
						Type:        "dashboard",
						Time:        time.Now(),
						Err:         err,
						Value:       status,
						Status: func(err error) Status {
							if err != nil {
								return StatusFail
							} else if statusCode >= 400 {
								return StatusWarning
							} else {
								return StatusPass
							}
						}(err),
					}
					return result
				},
			},
		},
		{
			id: VeidemannCrawlerStatus,
			checkers: []checker{
				func(ctx context.Context) *Result {
					var err error
					veidemannRunStatus, err = hc.controllerClient.GetRunStatus(ctx)
					result := &Result{
						Description: "check crawler status",
						Type:        "harvester",
						Time:        time.Now(),
						Err:         err,
						Value:       fmt.Sprintf("%v", veidemannRunStatus),
						Status: func(err error) Status {
							if err != nil {
								return StatusWarning
							} else {
								return StatusUndefined
							}
						}(err),
					}

					return result
				},
			},
		},
		{
			id: VeidemannJobs,
			checkers: []checker{
				func(ctx context.Context) *Result {
					result := &Result{
						Description: "check which jobs are running",
						Type:        "harvester",
						Time:        time.Now(),
					}
					var err error
					veidemannJobs, err = hc.controllerClient.GetRunningJobs(ctx)
					if err != nil {
						result.Err = err
						result.Status = func(err error) Status {
							if err != nil {
								return StatusWarning
							} else {
								return StatusUndefined
							}
						}(err)
					} else {
						if len(veidemannJobs) > 0 {
							result.Value = veidemannJobs
						}
					}
					return result
				},
			},
		},
		{
			id: VeidemannActivity,
			checkers: []checker{
				func(ctx context.Context) *Result {
					result := &Result{
						Description: "check if there is harvesting activity",
						Type:        "harvester",
						Time:        time.Now(),
					}
					var err error
					veidemannIsActive, err = hc.prometheusClient.IsActivity(ctx)
					if err != nil {
						result.Err = err
						result.Status = func(err error) Status {
							if err != nil {
								return StatusWarning
							} else {
								return StatusUndefined
							}
						}(err)
					} else {
						result.Value = veidemannIsActive
					}

					return result
				},
			},
		},
		{
			id: VeidemannHarvest,
			checkers: []checker{
				func(ctx context.Context) *Result {
					return &Result{
						Description: "check if veidemann harvest is nominal",
						Type:        "harvester",
						Time:        time.Now(),
						Status: func() Status {
							if veidemannRunStatus == nil {
								return StatusFail
							} else if *veidemannRunStatus == controllerApi.RunStatus_PAUSED {
								if veidemannIsActive {
									return StatusWarning
								} else {
									return StatusPass
								}
							} else if *veidemannRunStatus == controllerApi.RunStatus_PAUSE_REQUESTED {
								return StatusPass
							} else if len(veidemannJobs) > 0 && !veidemannIsActive {
								return StatusFail
							}
							return StatusPass
						}(),
					}
				},
			},
		},
	}
}
