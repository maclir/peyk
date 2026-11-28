#include <stdint.h>

#define PEYK_WILL_SLEEP 1
#define PEYK_POWERED_ON 2

int peyk_power_register(void);
void peyk_power_loop(void);
void peyk_power_allow(long id);
int peyk_power_capabilities(void);
void peyk_power_stay_awake(uint32_t *idle_id, uint32_t *system_id);
void peyk_power_release(uint32_t id);
