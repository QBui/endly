package endly

import (
	"fmt"
	"github.com/pkg/errors"
	"github.com/viant/endly/common"
	"net/url"
)


const BuildServiceId = "build"

type OperatingSystemDeployment struct {
	OsTarget *OperatingSystemTarget
	Config   *DeploymentConfig
}


type BuildGoal struct {
	Name                string
	Command             *ManagedCommand
	Transfers           *TransfersRequest
	VerificationCommand *ManagedCommand
}


type BuildMeta struct {
	Name             string
	Goals            []*BuildGoal
	goalsIndex       map[string]*BuildGoal
	BuildDeployments []*OperatingSystemDeployment //defines deployment of the build app itself, i.e how to get maven installed
}

func (m *BuildMeta) Validate() error {
	if m.Name == "" {
		return fmt.Errorf("MetaBuild.Names %v", m.Name)

	}
	if len(m.Goals) == 0 {
		return fmt.Errorf("MetaBuild.Goals were empty %v", m.Name)
	}
	return nil
}

func (m *BuildMeta) Match(operatingSystem *OperatingSystem, version string) *OperatingSystemDeployment {
	for _, candidate := range m.BuildDeployments {
		osTarget := candidate.OsTarget
		if version != "" {
			if candidate.Config.Transfer.Target.Version != version {
				continue
			}
		}
		if operatingSystem.Matches(osTarget) {
			return candidate
		}
	}
	return nil
}


type BuildSpec struct {
	Name    string //build name  like go, mvn, node, yarn
	Version string
	Goal    string //actual build target, like clean, test
	Args    string // additional build arguments , that can be expanded with $build.args
}

type BuildRequest struct {
	BuildSpec *BuildSpec //build specification
	Target    *Resource  //path to application to be build, Note that command may use $build.target variable. that expands to Target URL path
}


type BuildRegisterMeta struct {
	Meta *BuildMeta
}


type BuildLoadMeta struct {
	Resource *Resource
}

type BuildService struct {
	*AbstractService
	registry BuildMetaRegistry
}

func (s *BuildService) build(context *Context, request *BuildRequest) (interface{}, error) {
	target, err := context.ExpandResource(request.Target)
	if err != nil {
		return nil, err
	}
	buildSepc := request.BuildSpec

	if buildSepc == nil {
		return nil, fmt.Errorf("BuildSepc was empty")
	}
	buildMeta, has := s.registry[buildSepc.Name]
	if !has {
		return nil, fmt.Errorf("Failed to lookup build: %v", buildSepc.Name)
	}

	goal, has := buildMeta.goalsIndex[buildSepc.Goal]
	if !has {
		return nil, fmt.Errorf("Failed to lookup build %v goal: %v", buildSepc.Name, buildSepc.Goal)
	}

	parsedUrl, err := url.Parse(target.URL)
	if err != nil {
		return nil, err
	}

	err = setBuildState(buildSepc, parsedUrl, request, context)
	if err != nil {
		return nil, err
	}
	execService, err := context.Service(ExecServiceId)
	if err != nil {
		return nil, err
	}
	response := execService.Run(context, &OpenSession{
		Target: target,
	})

	if response.Error != "" {
		return nil, errors.New(response.Error)
	}

	operatingSystem := context.OperatingSystem(target.Session())
	buildDeployment := buildMeta.Match(operatingSystem, buildSepc.Version)
	if buildDeployment == nil {
		return nil, fmt.Errorf("Failed to find a build for provided operating system: %v %v", operatingSystem.Name, operatingSystem.Version)
	}

	deploymentService, err := context.Service(DeploymentServiceId)

	if err != nil {
		return nil, err
	}

	response = deploymentService.Run(context, buildDeployment.Config)
	if response.Error != "" {
		return nil, errors.New(response.Error)

	}

	_, err = context.Execute(target, goal.Command)
	if err != nil {
		return nil, err
	}

	if goal.Transfers != nil {
		_, err = context.Transfer(goal.Transfers.Transfers...)
		if err != nil {
			return nil, err
		}
	}
	if goal.VerificationCommand != nil {
		_, err = context.Execute(target, goal.VerificationCommand)
		if err != nil {
			return nil, err
		}
	}
	return nil, nil
}
func setBuildState(buildSepc *BuildSpec, parsedUrl *url.URL, request *BuildRequest, context *Context) error {
	target, err := context.ExpandResource(request.Target)
	if err != nil {
		return err
	}
	build := common.NewMap()
	build.Put("args", buildSepc.Args)
	build.Put("target", parsedUrl.Path)
	build.Put("host", parsedUrl.Host)
	build.Put("credential", target.Credential)
	var state = context.State()
	state.Put("build", build)
	return nil
}

func (s *BuildService) Run(context *Context, request interface{}) *ServiceResponse {
	var response = &ServiceResponse{
		Status: "ok",
	}
	var err error
	switch actualRequest := request.(type) {
	case *BuildRequest:
		response.Response, err = s.build(context, actualRequest)
		if err != nil {
			response.Error = fmt.Sprintf("Failed to build: %v %v", actualRequest.Target.URL, err)
		}
	case *BuildRegisterMeta:
		s.registry.Register(actualRequest.Meta)

	default:
		response.Error = fmt.Sprintf("Unsupported request type: %T", request)
	}
	if response.Error != "" {
		response.Status = "error"
	}
	return response
}

func (s *BuildService) NewRequest(action string) (interface{}, error) {
	return &BuildRequest{}, nil
}


func NewBuildService() Service {
	var result  = &BuildService{
		registry:        make(map[string]*BuildMeta),
		AbstractService: NewAbstractService(BuildServiceId),
	}
	result.AbstractService.Service = result
	return result
}





type BuildMetaRegistry map[string]*BuildMeta
func indexBuildGoals(goals []*BuildGoal, index map[string]*BuildGoal) {
	if len(goals) == 0 {
		return
	}
	for _, goal := range goals {
		index[goal.Name] = goal
	}
}

func (r *BuildMetaRegistry) Register(meta *BuildMeta) error {
	err := meta.Validate()
	if err != nil {
		return nil
	}
	meta.goalsIndex = make(map[string]*BuildGoal)
	indexBuildGoals(meta.Goals, meta.goalsIndex)
	(*r)[meta.Name] = meta
	return nil
}
