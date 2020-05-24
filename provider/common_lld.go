package provider

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
	"github.com/tpretz/go-zabbix-api"
)

// common schema elements for all lld types
var lldCommonSchema = map[string]*schema.Schema{
	"hostid": &schema.Schema{
		Type:         schema.TypeString,
		Required:     true,
		ForceNew:     true,
		Description:  "Host ID",
		ValidateFunc: validation.StringMatch(regexp.MustCompile("^[0-9]+$"), "must be numeric"),
	},
	"delay": &schema.Schema{
		Type:         schema.TypeString,
		Optional:     true,
		ValidateFunc: validation.StringIsNotWhiteSpace,
		Default:      "3600",
		Description:  "LLD Delay period",
	},
	"key": &schema.Schema{
		Type:         schema.TypeString,
		Description:  "LLD KEY",
		ValidateFunc: validation.StringIsNotWhiteSpace,
		Required:     true,
	},
	"name": &schema.Schema{
		Type:         schema.TypeString,
		Description:  "LLD Name",
		ValidateFunc: validation.StringIsNotWhiteSpace,
		Required:     true,
	},
}

// Interface schema
var lldInterfaceSchema = map[string]*schema.Schema{
	"interfaceid": &schema.Schema{
		Type:        schema.TypeString,
		Optional:    true,
		Description: "Host Interface ID",
		Default:     "0",
	},
}

// Schema for preprocessor blocks
var lldPreprocessorSchema = &schema.Schema{
	Type:     schema.TypeList,
	Optional: true,
	Elem: &schema.Resource{
		Schema: map[string]*schema.Schema{
			"id": &schema.Schema{
				Type:     schema.TypeString,
				Computed: true,
			},
			"type": &schema.Schema{
				Type:         schema.TypeString,
				Required:     true,
				Description:  "Preprocessor type, zabbix identifier number",
				ValidateFunc: validation.StringMatch(regexp.MustCompile("^[0-9]+$"), "must be numeric"),
			},
			"params": &schema.Schema{
				Type: schema.TypeList,
				Elem: &schema.Schema{
					Type:         schema.TypeString,
					ValidateFunc: validation.StringIsNotWhiteSpace,
				},
				Optional:    true,
				Description: "Preprocessor parameters",
			},
			"error_handler": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				Default:  "",
			},
			"error_handler_params": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				Default:  "",
			},
		},
	},
}

// Function signature for context manipulation
type LLDHandler func(*schema.ResourceData, *zabbix.LLDRule)

// return a terraform CreateFunc
func lldGetCreateWrapper(c LLDHandler, r LLDHandler) schema.CreateFunc {
	return func(d *schema.ResourceData, m interface{}) error {
		return resourceLLDCreate(d, m, c, r)
	}
}

// return a terraform UpdateFunc
func lldGetUpdateWrapper(c LLDHandler, r LLDHandler) schema.UpdateFunc {
	return func(d *schema.ResourceData, m interface{}) error {
		return resourceLLDUpdate(d, m, c, r)
	}
}

// return a terraform ReadFunc
func lldGetReadWrapper(r LLDHandler) schema.ReadFunc {
	return func(d *schema.ResourceData, m interface{}) error {
		return resourceLLDRead(d, m, r)
	}
}

// Create lld Resource Handler
func resourceLLDCreate(d *schema.ResourceData, m interface{}, c LLDHandler, r LLDHandler) error {
	api := m.(*zabbix.API)

	lld := buildLLDObject(d)

	// run custom function
	c(d, lld)

	log.Trace("preparing lld object for create/update: %#v", lld)

	llds := []zabbix.LLDRule{*lld}

	err := api.LLDsCreate(llds)

	if err != nil {
		return err
	}

	log.Trace("created lld: %+v", llds[0])

	d.SetId(llds[0].ItemID)

	return resourceLLDRead(d, m, r)
}

// Update lld Resource Handler
func resourceLLDUpdate(d *schema.ResourceData, m interface{}, c LLDHandler, r LLDHandler) error {
	api := m.(*zabbix.API)

	lld := buildLLDObject(d)
	lld.ItemID = d.Id()

	// run custom function
	c(d, lld)

	log.Trace("preparing lld object for create/update: %#v", lld)

	llds := []zabbix.LLDRule{*lld}

	err := api.LLDsUpdate(llds)

	if err != nil {
		return err
	}

	return resourceLLDRead(d, m, r)
}

// Read lld Resource Handler
func resourceLLDRead(d *schema.ResourceData, m interface{}, r LLDHandler) error {
	api := m.(*zabbix.API)

	log.Debug("Lookup of lld with id %s", d.Id())

	llds, err := api.LLDsGet(zabbix.Params{
		"lldids":              []string{d.Id()},
		"selectPreprocessing": "extend",
	})

	if err != nil {
		return err
	}

	if len(llds) < 1 {
		d.SetId("")
		return nil
	}
	if len(llds) > 1 {
		return errors.New("multiple llds found")
	}
	lld := llds[0]

	log.Debug("Got lld: %+v", lld)

	d.SetId(lld.ItemID)
	d.Set("hostid", lld.HostID)
	d.Set("key", lld.Key)
	d.Set("name", lld.Name)
	d.Set("delay", lld.Delay)
	d.Set("preprocessor", flattenlldPreprocessors(lld))

	// run custom
	r(d, &lld)

	return nil
}

// Build the base lld Object
func buildLLDObject(d *schema.ResourceData) *zabbix.LLDRule {
	lld := zabbix.LLDRule{
		Key:    d.Get("key").(string),
		HostID: d.Get("hostid").(string),
		Name:   d.Get("name").(string),
		Delay:  d.Get("delay").(string),
	}
	lld.Preprocessors = lldGeneratePreprocessors(d)

	return &lld
}

// Generate preprocessor objects
func lldGeneratePreprocessors(d *schema.ResourceData) (preprocessors zabbix.Preprocessors) {
	preprocessorCount := d.Get("preprocessor.#").(int)
	preprocessors = make(zabbix.Preprocessors, preprocessorCount)

	for i := 0; i < preprocessorCount; i++ {
		prefix := fmt.Sprintf("preprocessor.%d.", i)
		params := d.Get(prefix + "params").([]interface{})
		pstrarr := make([]string, len(params))
		for i := 0; i < len(params); i++ {
			pstrarr[i] = params[i].(string)
		}

		preprocessors[i] = zabbix.Preprocessor{
			Type:               d.Get(prefix + "type").(string),
			Params:             strings.Join(pstrarr, "\n"),
			ErrorHandler:       d.Get(prefix + "error_handler").(string),
			ErrorHandlerParams: d.Get(prefix + "error_handler_params").(string),
		}
	}

	return
}

// Generate terraform flattened form of lld preprocessors
func flattenlldPreprocessors(lld zabbix.LLDRule) []interface{} {
	val := make([]interface{}, len(lld.Preprocessors))
	for i := 0; i < len(lld.Preprocessors); i++ {
		parr := strings.Split(lld.Preprocessors[i].Params, "\n")
		val[i] = map[string]interface{}{
			//"id": host.Interfaces[i].InterfaceID,
			"type":                 lld.Preprocessors[i].Type,
			"params":               parr,
			"error_handler":        lld.Preprocessors[i].ErrorHandler,
			"error_handler_params": lld.Preprocessors[i].ErrorHandlerParams,
		}
	}
	return val
}

// Delete lld Resource Handler
func resourceLLDDelete(d *schema.ResourceData, m interface{}) error {
	api := m.(*zabbix.API)
	return api.LLDDeleteByIds([]string{d.Id()})
}