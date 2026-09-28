package flow

func Components() []Component {
	return []Component{
		{
			Type: "file_input", Name: "文件输入", Category: "输入",
			Description: "读取一次文件内容，并把它解析成数据表。",
			Outputs:     []Port{{ID: "rows", Name: "数据表"}},
			Params: []Field{
				{ID: "fileName", Name: "文件名", Type: "string", Required: true},
				{ID: "format", Name: "格式", Type: "select", Required: true, Options: []string{"csv", "json"}},
				{ID: "content", Name: "内容", Type: "text", Required: true, Description: "CSV 首行必须是表头；JSON 必须是对象数组。"},
			},
		},
		{
			Type: "transform", Name: "转换", Category: "处理",
			Description: "对上游数据表执行过滤、排序或字段转换；多输入会按行汇合。",
			Inputs:      []Port{{ID: "rows", Name: "数据表", Multi: true}},
			Outputs:     []Port{{ID: "rows", Name: "数据表"}},
			Params: []Field{
				{ID: "operation", Name: "操作", Type: "select", Required: true, Options: []string{"passthrough", "filter_equals", "uppercase", "lowercase", "sort"}},
				{ID: "field", Name: "字段", Type: "string"},
				{ID: "value", Name: "比较值", Type: "string"},
			},
		},
		{
			Type: "file_output", Name: "文件输出", Category: "输出",
			Description: "把本轮实际收到的数据写到目标文件。",
			Inputs:      []Port{{ID: "rows", Name: "数据表", Multi: true}},
			Params: []Field{
				{ID: "path", Name: "输出路径", Type: "string", Required: true},
				{ID: "format", Name: "格式", Type: "select", Required: true, Options: []string{"csv", "json"}},
			},
		},
	}
}
