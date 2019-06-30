package handler

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/aws/aws-sdk-go/service/dynamodb/expression"
	"github.com/golang/protobuf/ptypes"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/smartatransit/fivepoints/api/v1/schedule"
	"github.com/smartatransit/fivepoints/pkg/martaapi"
)

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . DynamoQuerier
type DynamoQuerier interface {
	QueryWithContext(ctx aws.Context, input *dynamodb.QueryInput, opts ...request.Option) (*dynamodb.QueryOutput, error)
}

//go:generate go run github.com/maxbrunsfeld/counterfeiter/v6 . GetScheduleEndpoint
type GetScheduleEndpoint func(context.Context, *schedule.GetScheduleRequest) (*schedule.GetScheduleResponse, error)

func ValidateRequest(ctx context.Context, in *schedule.GetScheduleRequest) error {
	var errStrings []string
	if in == nil {
		errStrings = append(errStrings, "request body is nil")
	}
	if strings.TrimSpace(in.GetDirection()) == "" {
		errStrings = append(errStrings, "direction is nil")
	}
	if strings.TrimSpace(in.GetStation()) == "" {
		errStrings = append(errStrings, "station is nil")
	}
	if in.GetStartDate() == nil {
		errStrings = append(errStrings, "start date is nil")
	}
	if in.GetEndDate() == nil {
		errStrings = append(errStrings, "end date is nil")
	}
	if len(errStrings) != 0 {
		return errors.New(fmt.Sprintf("validation errors: %s", strings.Join(errStrings, ", ")))
	}
	return nil
}

func GetScheduleRequestToDynamoQuery(in *schedule.GetScheduleRequest, tableName string) (*dynamodb.QueryInput, error) {
	s, err := ptypes.Timestamp(in.GetStartDate())
	if err != nil {
		return nil, err
	}
	e, err := ptypes.Timestamp(in.GetEndDate())
	if err != nil {
		return nil, err
	}
	primaryKey := fmt.Sprintf("%s_%s_%s", in.GetStation(), in.GetDirection(), s.Format("2006-01-02"))
	keyCondition := expression.
		Key("PrimaryKey").
		Equal(expression.Value(primaryKey)).
		And(expression.Key("SortKey").
			Between(expression.Value(s), expression.Value(e)))
	expr, err := expression.NewBuilder().WithKeyCondition(keyCondition).Build()
	if err != nil {
		return nil, err
	}

	input := &dynamodb.QueryInput{
		TableName:                 aws.String(tableName),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		KeyConditionExpression:    expr.KeyCondition(),
	}
	return input, nil
}

func NewGetScheduleEndpoint(
	tableName string,
	querier DynamoQuerier,
) func(context.Context, *schedule.GetScheduleRequest) (*schedule.GetScheduleResponse, error) {
	return func(ctx context.Context, in *schedule.GetScheduleRequest) (*schedule.GetScheduleResponse, error) {
		var schedules []martaapi.Schedule
		//todo -- jwt authorization?

		err := ValidateRequest(ctx, in)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		queryInput, err := GetScheduleRequestToDynamoQuery(in, tableName)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		output, err := querier.QueryWithContext(ctx, queryInput)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if len(output.Items) == 0 {
			return &schedule.GetScheduleResponse{}, nil
		}
		err = dynamodbattribute.UnmarshalListOfMaps(output.Items, &schedules)
		if err != nil {
			return nil, err
		}
		return &schedule.GetScheduleResponse{
			Schedules: MartaSchedulesToProtoSchedules(schedules),
		}, nil
	}
}

func MartaSchedulesToProtoSchedules(martaScheds []martaapi.Schedule) []*schedule.Schedule {
	var protoSchedule []*schedule.Schedule
	for _, sched := range martaScheds {
		x := schedule.Schedule{
			PrimaryKey:     sched.PrimaryKey,
			SortKey:        sched.SortKey,
			Destination:    sched.Destination,
			Direction:      sched.Direction,
			EventTime:      sched.EventTime,
			Line:           sched.Line,
			NextArrival:    sched.NextArrival,
			Station:        sched.Station,
			TrainID:        sched.TrainID,
			WaitingSeconds: sched.WaitingSeconds,
			WaitingTime:    sched.WaitingTime,
			TTL:            sched.TTL,
		}
		protoSchedule = append(protoSchedule, &x)
	}
	return protoSchedule
}
